package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	sshTargetPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)
	ansiPattern      = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
)

func (s *Server) handleSetupInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	_, err := exec.LookPath("ssh")
	writeJSON(w, 200, map[string]any{
		"serverUrl":    s.setupBaseURL(r),
		"agentToken":   s.cfg.AgentToken,
		"sshAvailable": err == nil,
	})
}

func (s *Server) handleProvisionServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string `json:"name"`
		Target       string `json:"target"`
		Port         int    `json:"port"`
		IdentityFile string `json:"identityFile"`
		ServerURL    string `json:"serverUrl"`
	}
	if err := readJSON(r, &body, 64<<10); err != nil {
		apiErr(w, 400, err)
		return
	}
	target := strings.TrimSpace(body.Target)
	if target == "" || !sshTargetPattern.MatchString(target) {
		apiErr(w, 400, errText("SSH target must be a host, user@host or ~/.ssh/config alias"))
		return
	}
	if body.Port < 0 || body.Port > 65535 {
		apiErr(w, 400, errText("invalid port"))
		return
	}
	if body.IdentityFile != "" {
		st, err := os.Stat(body.IdentityFile)
		if err != nil || st.IsDir() {
			apiErr(w, 400, errText("identity file is not readable: "+body.IdentityFile))
			return
		}
	}
	base := strings.TrimSpace(body.ServerURL)
	if base == "" {
		base = s.setupBaseURL(r)
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		apiErr(w, 400, errText("control plane URL must start with http:// or https://"))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = target
		if i := strings.LastIndex(name, "@"); i >= 0 {
			name = name[i+1:]
		}
	}
	if len(name) > 64 {
		name = name[:64]
	}

	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		apiErr(w, 400, errText("ssh is not installed on the control plane machine"))
		return
	}
	script, err := os.ReadFile(s.bootstrapPath())
	if err != nil {
		apiErr(w, 500, fmt.Errorf("bootstrap script is missing: %w", err))
		return
	}

	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ServerAliveInterval=15",
	}
	if body.Port > 0 {
		args = append(args, "-p", strconv.Itoa(body.Port))
	}
	if body.IdentityFile != "" {
		args = append(args, "-i", body.IdentityFile, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, target, "bash -s -- "+remoteBootstrapArgs(base, s.cfg.AgentToken, name))

	// Do not tie a multi-minute package install to the browser request. A
	// Cloudflare edge timeout or a closed tab would otherwise kill the SSH
	// process halfway through the bootstrap. The output is still returned when
	// the install finishes; a later phase should turn this into a job.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, sshBin, args...)
	cmd.Stdin = bytes.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()

	output := ansiPattern.ReplaceAllString(out.String(), "")
	if len(output) > 128<<10 {
		output = output[:128<<10] + "\n[truncated]"
	}
	if runErr != nil {
		_ = s.st.AddEvent("ui", "server.provision_failed", target, runErr.Error())
		writeJSON(w, 502, map[string]any{"ok": false, "error": runErr.Error(), "output": output})
		return
	}
	_ = s.st.AddEvent("ui", "server.provision", target, name)
	writeJSON(w, 200, map[string]any{"ok": true, "output": output})
}

func (s *Server) bootstrapPath() string {
	candidates := []string{
		filepath.Join(s.cfg.BinDir(), "bootstrap.sh"),
		filepath.Join("scripts", "bootstrap-termux.sh"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return candidates[0]
}

func remoteBootstrapArgs(base, token, name string) string {
	return fmt.Sprintf("--server %s --token %s --name %s",
		shellQuote(base), shellQuote(token), shellQuote(name))
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// setupBaseURL returns the URL a phone should use. Prefer the configured
// public edge, then the address the browser used, then a private LAN IP.
func (s *Server) setupBaseURL(r *http.Request) string {
	if publicURL, _ := s.Edge(); publicURL != "" {
		return strings.TrimRight(publicURL, "/")
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := normalizeHost(r.Host)
	port := "8080"
	if _, p, err := net.SplitHostPort(r.Host); err == nil {
		port = p
	}
	if host != "" && host != "localhost" && !strings.HasPrefix(host, "127.") && host != "::1" {
		return scheme + "://" + r.Host
	}
	if ip := firstPrivateIPv4(); ip != "" {
		return fmt.Sprintf("%s://%s:%s", scheme, ip, port)
	}
	return scheme + "://" + r.Host
}

func firstPrivateIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	type candidate struct {
		ip    string
		score int
	}
	var out []candidate
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		name := strings.ToLower(iface.Name)
		if strings.Contains(name, "vethernet") || strings.Contains(name, "vmware") || strings.Contains(name, "virtual") || strings.Contains(name, "loopback") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
				continue
			}
			score := 30
			s := ip.String()
			if strings.HasPrefix(s, "192.168.") {
				score = 0
			} else if strings.HasPrefix(s, "10.") {
				score = 10
			}
			out = append(out, candidate{ip: s, score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].score < out[j].score })
	if len(out) > 0 {
		return out[0].ip
	}
	return ""
}
