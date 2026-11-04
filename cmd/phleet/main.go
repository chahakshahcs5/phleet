// Command phleet is the operator CLI: Cloudflare setup, health checks and
// quick server listings.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"phleet/internal/cloudflare"
	"phleet/internal/config"
	"phleet/internal/secrets"
	"phleet/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "setup-cloudflare":
		os.Exit(setupCloudflare(os.Args[2:]))
	case "doctor":
		os.Exit(doctor(os.Args[2:]))
	case "servers":
		os.Exit(servers(os.Args[2:]))
	case "token":
		os.Exit(printTokens(os.Args[2:]))
	case "session":
		os.Exit(revokeSessions(os.Args[2:]))
	case "backup":
		os.Exit(backupDB(os.Args[2:]))
	case "restore-backup":
		os.Exit(restoreBackup(os.Args[2:]))
	case "deploy":
		os.Exit(cliDeploy(os.Args[2:]))
	case "logs":
		os.Exit(cliLogs(os.Args[2:]))
	case "exec":
		os.Exit(cliExec(os.Args[2:]))
	case "totp":
		os.Exit(totpCommand(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Println("phleet " + config.Version)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`phleet — control plane CLI

  phleet setup-cloudflare -domain example.com [flags]
      Create the tunnel, wildcard DNS and (optionally) the cloudflared service.

  phleet doctor [flags]        Check tunnel, DNS and local control plane health.
  phleet servers [flags]       List registered phones.
  phleet token [-which admin|agent|both] [-rotate]
                               Print tokens, or generate new token file(s)
  phleet session -revoke       Invalidate every existing panel session
  phleet backup [-out FILE]    Write an encrypted snapshot of phleet.db
  phleet restore-backup -in FILE -out DB [-force]
                               Decrypt a snapshot for restore
  phleet deploy -project ID [-ref REF] [-wait]
  phleet logs -project ID [-server ID] [-bytes N]
  phleet exec -server ID -command "..." [-timeout SEC]
  phleet totp -disable         Break-glass: remove the admin TOTP secret

Flags:
  -data DIR        data directory (default ` + config.DefaultDataDir() + `)
  -local URL       local control plane URL (default http://127.0.0.1:8080)

setup-cloudflare flags:
  -domain DOMAIN           required, the Cloudflare zone
  -token TOKEN             API token, or CLOUDFLARE_API_TOKEN
  -tunnel NAME             tunnel name (default phleet)
  -install                 install/restart cloudflared as a service (default true)
  -cloudflared PATH        cloudflared binary (default from PATH)

API token scopes: Zone:Read, DNS:Edit, Account:Cloudflare Tunnel:Edit
Environment: CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID (optional)`)
}

func setupCloudflare(args []string) int {
	fs := flag.NewFlagSet("setup-cloudflare", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	domain := fs.String("domain", "", "Cloudflare zone, e.g. example.com")
	token := fs.String("token", os.Getenv("CLOUDFLARE_API_TOKEN"), "Cloudflare API token")
	tunnelName := fs.String("tunnel", "phleet", "tunnel name")
	local := fs.String("local", "http://127.0.0.1:8080", "local control plane URL")
	install := fs.Bool("install", true, "install and start cloudflared as a service")
	cfBin := fs.String("cloudflared", "cloudflared", "cloudflared binary")
	_ = fs.Parse(args)

	if *domain == "" {
		fmt.Fprintln(os.Stderr, "error: -domain is required")
		return 2
	}
	*domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(*domain, ".")))
	if strings.HasPrefix(*domain, "www.") {
		*domain = strings.TrimPrefix(*domain, "www.")
	}
	if *token == "" {
		fmt.Fprintln(os.Stderr, "error: set CLOUDFLARE_API_TOKEN or pass -token")
		return 2
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cf := cloudflare.New(*token)

	step("verifying API token")
	if err := cf.Verify(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		fmt.Fprintln(os.Stderr, "  The token needs: Zone:Read, DNS:Edit, Account:Cloudflare Tunnel:Edit")
		return 1
	}
	ok("token is active")

	step("looking up zone " + *domain)
	zone, err := cf.Zone(ctx, *domain)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		return 1
	}
	ok("zone " + zone.ID + " (" + zone.Status + ")")
	if !strings.EqualFold(zone.Status, "active") {
		fmt.Println("  ! zone is not active yet; DNS records can be created but resolve only after")
		fmt.Println("    the registrar points at: " + strings.Join(zone.NameServers, ", "))
	}

	accountID := zone.Account.ID
	if v := os.Getenv("CLOUDFLARE_ACCOUNT_ID"); v != "" {
		accountID = v
	}

	step("finding tunnel " + *tunnelName)
	tunnels, err := cf.Tunnels(ctx, accountID, *tunnelName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		return 1
	}
	var tun cloudflare.Tunnel
	if len(tunnels) > 0 {
		tun = tunnels[0]
		ok("reusing tunnel " + tun.ID)
	} else {
		tun, err = cf.CreateTunnel(ctx, accountID, *tunnelName)
		if err != nil {
			fmt.Fprintln(os.Stderr, "  ✗", err)
			return 1
		}
		ok("created tunnel " + tun.ID)
	}

	step("fetching tunnel token")
	tunnelToken, err := cf.TunnelToken(ctx, accountID, tun.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		return 1
	}
	ok("token received")

	step("configuring ingress")
	ingress := []cloudflare.Ingress{
		{Hostname: "fleet." + *domain, Service: *local},
		{Service: *local}, // catch-all: phleetd routes by Host
	}
	if err := cf.PutConfiguration(ctx, accountID, tun.ID, ingress); err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		return 1
	}
	ok("fleet." + *domain + " and *." + *domain + " -> " + *local)

	step("upserting DNS records")
	target := tun.ID + ".cfargotunnel.com"
	for _, name := range []string{"fleet." + *domain, "*." + *domain} {
		res, err := cf.UpsertCNAME(ctx, zone.ID, name, target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", name, err)
			return 1
		}
		ok(fmt.Sprintf("%s CNAME %s (%s)", name, target, res))
	}

	conf := cloudflare.Config{
		Domain:      *domain,
		ZoneID:      zone.ID,
		AccountID:   accountID,
		TunnelID:    tun.ID,
		TunnelName:  *tunnelName,
		APIToken:    *token,
		TunnelToken: tunnelToken,
		LocalURL:    *local,
	}
	if err := cloudflare.SaveConfig(cfg.CloudflaredConfig(), conf); err != nil {
		fmt.Fprintln(os.Stderr, "error saving config:", err)
		return 1
	}
	ok("saved " + cfg.CloudflaredConfig())

	if *install {
		step("installing cloudflared service")
		if _, err := exec.LookPath(*cfBin); err != nil {
			fmt.Printf("  ! %s not found in PATH\n", *cfBin)
			fmt.Println("    install it from https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/")
			fmt.Printf("    then run: %s service install %s\n", *cfBin, tunnelToken)
		} else {
			cmd := exec.Command(*cfBin, "service", "install", tunnelToken)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				fmt.Println("  ! service install failed:", err)
				if runtime.GOOS == "windows" {
					fmt.Println("    run this terminal as Administrator, or install the service manually:")
				}
				fmt.Printf("    %s service install %s\n", *cfBin, tunnelToken)
			} else {
				ok("service installed")
			}
		}
	}

	fmt.Println()
	fmt.Println("  Done. Public endpoints:")
	fmt.Printf("    panel      https://fleet.%s\n", *domain)
	fmt.Printf("    projects   https://<project>.%s\n", *domain)
	fmt.Println()
	fmt.Println("  Next:")
	fmt.Println("    1. Start phleetd (it should already be running on " + *local + ")")
	fmt.Println("    2. Protect the panel with Cloudflare Access:")
	fmt.Println("       Zero Trust > Access > Applications > Add > Self-hosted > fleet." + *domain)
	fmt.Println("       Add a bypass policy for path /agent so phones can connect.")
	fmt.Printf("    3. On each phone: bootstrap-termux.sh --server https://fleet.%s --token <agent-token>\n", *domain)
	fmt.Println("       agent token: " + filepath.Join(cfg.DataDir, config.AgentFile))
	return 0
}

func doctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	local := fs.String("local", "", "local control plane URL (default from config)")
	_ = fs.Parse(args)

	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	conf, err := cloudflare.LoadConfig(cfg.CloudflaredConfig())
	if err != nil {
		fmt.Println("cloudflare: not configured (run: phleet setup-cloudflare -domain example.com)")
		conf = cloudflare.Config{LocalURL: "http://127.0.0.1:8080"}
	}
	target := conf.LocalURL
	if *local != "" {
		target = *local
	}
	fail := 0

	step("local control plane " + target)
	hc, err := getJSON(target+"/healthz", "", 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "  ✗", err)
		fail++
	} else {
		ok(fmt.Sprintf("up, %v agents online, %v metric rows", hc["agentsOnline"], hc["metricRows"]))
	}

	if conf.TunnelID != "" && conf.APIToken != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cf := cloudflare.New(conf.APIToken)
		step("tunnel " + conf.TunnelID)
		t, err := cf.Tunnel(ctx, conf.AccountID, conf.TunnelID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "  ✗", err)
			fail++
		} else {
			ok("status: " + t.Status + " (healthy means a connector is connected)")
			if t.Status != "healthy" {
				fmt.Println("  ! is cloudflared running?  sc query cloudflared / systemctl status cloudflared")
			}
		}

		step("DNS")
		for _, name := range []string{"fleet." + conf.Domain, "*." + conf.Domain} {
			ctx2, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
			ips, err := net.DefaultResolver.LookupHost(ctx2, name)
			cancel2()
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", name, err)
				fail++
			} else {
				ok(name + " -> " + strings.Join(ips, ", "))
			}
		}

		step("nameservers")
		ctx3, cancel3 := context.WithTimeout(context.Background(), 8*time.Second)
		ns, err := net.DefaultResolver.LookupNS(ctx3, conf.Domain)
		cancel3()
		if err != nil || len(ns) == 0 {
			fmt.Fprintln(os.Stderr, "  ✗ could not resolve NS:", err)
			fail++
		} else {
			got := nsHosts(ns)
			sort.Strings(got)
			fmt.Println("  · resolver NS:", strings.Join(got, ", "))
			if !strings.Contains(strings.Join(got, ","), ".ns.cloudflare.com") {
				fmt.Println("  ! nameservers are not Cloudflare; move the domain to Cloudflare")
				fail++
			} else {
				ok("nameservers look like Cloudflare")
			}
		}

		step("public endpoint https://fleet." + conf.Domain)
		hc2, err := getJSON("https://fleet."+conf.Domain+"/healthz", "", 12*time.Second)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ! %v\n", err)
			fmt.Println("    (expected if Cloudflare Access protects the panel)")
		} else {
			ok(fmt.Sprintf("reachable, %v agents online", hc2["agentsOnline"]))
		}
	}

	fmt.Println()
	if fail > 0 {
		fmt.Printf("  %d check(s) need attention\n", fail)
		return 1
	}
	fmt.Println("  all checks passed")
	return 0
}

func servers(args []string) int {
	fs := flag.NewFlagSet("servers", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	local := fs.String("local", "", "local control plane URL")
	jsonOut := fs.Bool("json", false, "raw JSON")
	_ = fs.Parse(args)

	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	base := *local
	if base == "" {
		if conf, err := cloudflare.LoadConfig(cfg.CloudflaredConfig()); err == nil && conf.LocalURL != "" {
			base = conf.LocalURL
		} else {
			base = "http://127.0.0.1:8080"
		}
	}
	var resp struct {
		Servers []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
			Online bool   `json:"online"`
			Latest *struct {
				CPUPercent float64 `json:"cpuPercent"`
				TempC      float64 `json:"tempC"`
				BatteryPct int     `json:"batteryPct"`
			} `json:"latest"`
		} `json:"servers"`
	}
	if err := getInto(base+"/api/servers", cfg.AdminToken, &resp); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *jsonOut {
		b, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(resp.Servers) == 0 {
		fmt.Println("no phones registered yet")
		return 0
	}
	fmt.Printf("%-38s %-18s %-8s %6s %6s %5s\n", "ID", "NAME", "STATUS", "CPU%", "TEMP", "BATT")
	for _, s := range resp.Servers {
		cpu, temp, batt := 0.0, 0.0, -1
		if s.Latest != nil {
			cpu, temp, batt = s.Latest.CPUPercent, s.Latest.TempC, s.Latest.BatteryPct
		}
		fmt.Printf("%-38s %-18s %-8s %6.1f %6.1f %5d\n", s.ID, s.Name, s.Status, cpu, temp, batt)
	}
	return 0
}

func printTokens(args []string) int {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	which := fs.String("which", "both", "admin|agent|both")
	rotate := fs.Bool("rotate", false, "generate new token(s) and write the token file(s)")
	_ = fs.Parse(args)
	if *which != "admin" && *which != "agent" && *which != "both" {
		fmt.Fprintln(os.Stderr, "error: -which must be admin, agent or both")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	tokens := map[string]string{
		"admin": cfg.AdminToken,
		"agent": cfg.AgentToken,
	}
	if *rotate {
		for _, target := range []struct{ name, file string }{
			{"admin", config.AdminFile},
			{"agent", config.AgentFile},
		} {
			if *which != "both" && *which != target.name {
				continue
			}
			tok, err := rotateTokenFile(filepath.Join(cfg.DataDir, target.file))
			if err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				return 1
			}
			tokens[target.name] = tok
		}
		if *which == "admin" || *which == "both" {
			if err := bumpSessionEpoch(cfg); err != nil {
				fmt.Fprintln(os.Stderr, "warning: admin sessions were not revoked:", err)
			}
		}
		fmt.Fprintln(os.Stderr, "token file(s) updated; restart phleetd for the new token to take effect")
	}
	if *which == "admin" || *which == "both" {
		fmt.Println(tokens["admin"])
	}
	if *which == "agent" || *which == "both" {
		fmt.Println(tokens["agent"])
	}
	return 0
}

func revokeSessions(args []string) int {
	fs := flag.NewFlagSet("session", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	revoke := fs.Bool("revoke", false, "invalidate every existing panel session")
	_ = fs.Parse(args)
	if !*revoke {
		fmt.Fprintln(os.Stderr, "usage: phleet session -revoke")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := bumpSessionEpoch(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println("all panel sessions revoked")
	return 0
}

func bumpSessionEpoch(cfg *config.Config) error {
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()
	return st.SetSetting(config.SessionEpochSetting, strconv.FormatInt(time.Now().UnixNano(), 10))
}

func rotateTokenFile(path string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tok, nil
}

func backupDB(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	out := fs.String("out", "", "output file (default <data>/backups/phleet-<timestamp>.db.enc)")
	encrypt := fs.Bool("encrypt", true, "encrypt the snapshot with session.key")
	plaintext := fs.Bool("plaintext", false, "write an unencrypted snapshot (not recommended)")
	_ = fs.Parse(args)
	if *plaintext {
		*encrypt = false
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *out == "" {
		suffix := ".db.enc"
		if !*encrypt {
			suffix = ".db"
		}
		*out = filepath.Join(cfg.DataDir, "backups", "phleet-"+time.Now().Format("20060102-150405")+suffix)
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	liveDB, _ := filepath.Abs(cfg.DBPath())
	if filepath.Clean(abs) == filepath.Clean(liveDB) {
		fmt.Fprintln(os.Stderr, "error: -out must not be the live database")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer st.Close()

	if !*encrypt {
		if err := st.Backup(abs); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		fmt.Println(abs)
		return 0
	}

	plain := abs + ".plain"
	_ = os.Remove(plain)
	if err := st.Backup(plain); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	_ = os.Chmod(plain, 0o600)
	if _, err := secrets.EncryptFile(cfg.SessionKey, plain, abs); err != nil {
		_ = os.Remove(plain)
		_ = os.Remove(abs)
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	_ = os.Remove(plain)
	fmt.Println(abs)
	return 0
}

func restoreBackup(args []string) int {
	fs := flag.NewFlagSet("restore-backup", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	in := fs.String("in", "", "encrypted backup file")
	out := fs.String("out", "", "destination database path")
	force := fs.Bool("force", false, "overwrite the destination if it exists")
	_ = fs.Parse(args)
	if *in == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "error: -in and -out are required")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	src, err := os.Open(*in)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer src.Close()
	dst, err := filepath.Abs(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	inAbs, _ := filepath.Abs(*in)
	if filepath.Clean(inAbs) == filepath.Clean(dst) {
		fmt.Fprintln(os.Stderr, "error: -in and -out must be different files")
		return 1
	}
	if st, err := os.Stat(dst); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "error: %s already exists (use -force to overwrite)\n", dst)
		return 1
	} else if err == nil && st.IsDir() {
		fmt.Fprintln(os.Stderr, "error: -out must be a file")
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := secrets.DecryptTo(cfg.SessionKey, src, f); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	if *force {
		_ = os.Remove(dst)
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Println(dst)
	return 0
}

func totpCommand(args []string) int {
	fs := flag.NewFlagSet("totp", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	disable := fs.Bool("disable", false, "remove the admin TOTP secret (break-glass)")
	_ = fs.Parse(args)
	if !*disable {
		fmt.Fprintln(os.Stderr, "only -disable is supported; enable 2FA from Settings → Security")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	defer st.Close()
	_ = st.DeleteSetting("admin_totp_secret")
	_ = st.DeleteSetting("admin_totp_pending")
	_ = st.DeleteSetting("admin_totp_last_step")
	fmt.Println("admin TOTP disabled")
	return 0
}

// ---- small helpers ----

func step(msg string) { fmt.Println("→ " + msg) }
func ok(msg string)   { fmt.Println("  ✓ " + msg) }

func getJSON(url, token string, timeout time.Duration) (map[string]any, error) {
	var out map[string]any
	err := getIntoTimeout(url, token, timeout, &out)
	return out, err
}

func getInto(url, token string, out any) error {
	return getIntoTimeout(url, token, 10*time.Second, out)
}

func controlPlaneBase(cfg *config.Config, local string) string {
	if strings.TrimSpace(local) != "" {
		return strings.TrimRight(local, "/")
	}
	if conf, err := cloudflare.LoadConfig(cfg.CloudflaredConfig()); err == nil && conf.LocalURL != "" {
		return strings.TrimRight(conf.LocalURL, "/")
	}
	return "http://127.0.0.1:8080"
}

func postJSON(url, token string, body any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func cliDeploy(args []string) int {
	fs := flag.NewFlagSet("deploy", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	local := fs.String("local", "", "control plane URL")
	project := fs.String("project", "", "project ID")
	ref := fs.String("ref", "", "branch, tag or commit SHA (default: configured branch)")
	wait := fs.Bool("wait", false, "wait for the deployment to finish")
	_ = fs.Parse(args)
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "error: -project is required")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	base := controlPlaneBase(cfg, *local)
	var res struct {
		Deployments []struct {
			ID       string `json:"id"`
			ServerID string `json:"serverId"`
			Status   string `json:"status"`
		} `json:"deployments"`
		Skipped []string `json:"skipped"`
	}
	body := map[string]any{}
	if strings.TrimSpace(*ref) != "" {
		body["ref"] = strings.TrimSpace(*ref)
	}
	if _, err := postJSON(base+"/api/projects/"+*project+"/deploy", cfg.AdminToken, body, &res); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for _, d := range res.Deployments {
		fmt.Printf("%s %s %s\n", d.ID, d.ServerID, d.Status)
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(os.Stderr, "skipped offline phones: %s\n", strings.Join(res.Skipped, ", "))
	}
	if !*wait || len(res.Deployments) == 0 {
		return 0
	}
	deadline := time.Now().Add(20 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		var list struct {
			Deployments []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			} `json:"deployments"`
		}
		if err := getInto(base+"/api/projects/"+*project+"/deployments", cfg.AdminToken, &list); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		terminal, failed := 0, false
		for _, d := range list.Deployments {
			switch d.Status {
			case "active":
				terminal++
			case "failed", "rolled_back":
				terminal++
				failed = true
				if d.Detail != "" {
					fmt.Fprintln(os.Stderr, d.Detail)
				}
			}
		}
		if terminal >= len(res.Deployments) {
			if failed {
				return 1
			}
			fmt.Println("deployment active")
			return 0
		}
	}
	fmt.Fprintln(os.Stderr, "timed out waiting for the deployment")
	return 1
}

func cliLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	local := fs.String("local", "", "control plane URL")
	project := fs.String("project", "", "project ID")
	server := fs.String("server", "", "server ID")
	size := fs.Int("bytes", 64<<10, "tail size")
	_ = fs.Parse(args)
	if strings.TrimSpace(*project) == "" {
		fmt.Fprintln(os.Stderr, "error: -project is required")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	base := controlPlaneBase(cfg, *local)
	url := base + "/api/projects/" + *project + "/logs?bytes=" + fmt.Sprint(*size)
	if strings.TrimSpace(*server) != "" {
		url += "&serverId=" + *server
	}
	var res struct {
		Logs string `json:"logs"`
		Path string `json:"path"`
	}
	if err := getInto(url, cfg.AdminToken, &res); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Print(res.Logs)
	if !strings.HasSuffix(res.Logs, "\n") {
		fmt.Println()
	}
	return 0
}

func cliExec(args []string) int {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	dataDir := fs.String("data", config.DefaultDataDir(), "data directory")
	local := fs.String("local", "", "control plane URL")
	server := fs.String("server", "", "server ID")
	command := fs.String("command", "", "shell command")
	timeout := fs.Int("timeout", 30, "timeout in seconds")
	_ = fs.Parse(args)
	if strings.TrimSpace(*server) == "" || strings.TrimSpace(*command) == "" {
		fmt.Fprintln(os.Stderr, "error: -server and -command are required")
		return 2
	}
	cfg, err := config.Load(*dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	base := controlPlaneBase(cfg, *local)
	var res struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exitCode"`
	}
	if _, err := postJSON(base+"/api/servers/"+*server+"/exec", cfg.AdminToken, map[string]any{
		"command": *command, "timeoutSec": *timeout,
	}, &res); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	fmt.Print(res.Output)
	if !strings.HasSuffix(res.Output, "\n") {
		fmt.Println()
	}
	if res.ExitCode < 0 || res.ExitCode > 255 {
		return 1
	}
	return res.ExitCode
}

func getIntoTimeout(url, token string, timeout time.Duration, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	c := &http.Client{Timeout: timeout}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s returned HTTP %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func nsHosts(ns []*net.NS) []string {
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		out = append(out, strings.ToLower(strings.TrimSuffix(n.Host, ".")))
	}
	return out
}
