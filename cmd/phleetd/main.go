// Command phleetd is the control plane: API, UI, agent hub and router.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"phleet/internal/cloudflare"
	"phleet/internal/config"
	"phleet/internal/httpapi"
	"phleet/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "listen address (keep it on loopback when using a tunnel)")
	dataDir := flag.String("data", config.DefaultDataDir(), "data directory")
	publicURL := flag.String("public-url", "", "public base URL, e.g. https://fleet.example.com")
	printTokens := flag.Bool("print-tokens", false, "print secrets to stdout on startup")
	debug := flag.Bool("debug", false, "debug logging")
	bridgeLAN := flag.Bool("bridge-lan", false, "bind addon TCP bridges to 0.0.0.0 instead of loopback")
	storageMaxMB := flag.Int("storage-max-mb", 96, "max S3 request body in MB (96 matches Cloudflare; raise it for direct/LAN use)")
	flag.Parse()

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(*dataDir)
	if err != nil {
		log.Error("cannot initialise data directory", "err", err)
		os.Exit(1)
	}
	cfg.Addr = *addr
	cfg.PublicURL = *publicURL
	cfg.BridgeLAN = *bridgeLAN
	cfg.StorageMaxBody = int64(*storageMaxMB) << 20

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		log.Error("cannot open database", "err", err, "path", cfg.DBPath())
		os.Exit(1)
	}
	defer st.Close()
	if err := st.ResetServersOffline(); err != nil {
		log.Warn("reset server status", "err", err)
	}
	if err := st.ResetAddonsOffline(); err != nil {
		log.Warn("reset addon status", "err", err)
	}
	publishAssets(cfg, log)

	api := httpapi.New(cfg, st, log)
	edgeURL, fleetHost := edgeFromConfig(cfg)
	if edgeURL != "" {
		cfg.PublicURL = edgeURL
		api.SetEdge(edgeURL, fleetHost)
	}
	handler := api.Handler()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go janitor(ctx, st, log)
	go watchEdge(ctx, cfg, api, log)
	api.StartBackground(ctx)

	go func() {
		log.Info("phleet control plane listening", "addr", cfg.Addr, "version", config.Version, "data", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			stop()
		}
	}()

	printBanner(cfg, *printTokens)

	<-ctx.Done()
	log.Info("shutting down")
	api.Hub().CloseAll()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func printBanner(cfg *config.Config, tokens bool) {
	fmt.Println()
	fmt.Println("  phleet " + config.Version)
	fmt.Println("  ─────────────────────────────────────────────")
	fmt.Printf("  UI        http://%s/\n", cfg.Addr)
	fmt.Printf("  data      %s\n", cfg.DataDir)
	fmt.Printf("  admin     %s\n", filepath.Join(cfg.DataDir, config.AdminFile))
	fmt.Printf("  agent     %s\n", filepath.Join(cfg.DataDir, config.AgentFile))
	if cfg.PublicURL != "" {
		fmt.Printf("  public    %s\n", strings.TrimRight(cfg.PublicURL, "/"))
	}
	if tokens {
		fmt.Println()
		fmt.Printf("  admin token: %s\n", cfg.AdminToken)
		fmt.Printf("  agent token: %s\n", cfg.AgentToken)
	}
	fmt.Println()
}

func janitor(ctx context.Context, st *store.Store, log *slog.Logger) {
	status := time.NewTicker(15 * time.Second)
	defer status.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-status.C:
			n, err := st.MarkStaleOffline(time.Now().Add(-45 * time.Second).Unix())
			if err != nil {
				log.Warn("mark stale offline", "err", err)
			} else if n > 0 {
				log.Info("marked servers offline", "count", n)
			}
		case <-prune.C:
			cutoff := time.Now().Add(-14 * 24 * time.Hour).Unix()
			if err := st.PruneMetrics(cutoff); err != nil {
				log.Warn("prune metrics", "err", err)
			}
			if err := st.PruneAppMetrics(cutoff); err != nil {
				log.Warn("prune app metrics", "err", err)
			}
			if err := st.PruneRequestLogs(time.Now().Add(-7 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune request logs", "err", err)
			}
			if err := st.PruneProjectHistory(time.Now().Add(-30 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune deployment history", "err", err)
			}
			if err := st.PruneAlerts(time.Now().Add(-90 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune alerts", "err", err)
			}
			if err := st.PruneDrills(time.Now().Add(-90 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune restore drills", "err", err)
			}
			if err := st.PruneAddonLogs(time.Now().Add(-30 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune addon logs", "err", err)
			}
			if err := st.PruneEvents(time.Now().Add(-90 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune events", "err", err)
			}
			if err := st.PruneJobs(time.Now().Add(-7 * 24 * time.Hour).Unix()); err != nil {
				log.Warn("prune jobs", "err", err)
			}
		}
	}
}

// edgeFromConfig reads the tunnel configuration written by
// `phleet setup-cloudflare`. Missing config means "local only".
func edgeFromConfig(cfg *config.Config) (publicURL, fleetHost string) {
	conf, err := cloudflare.LoadConfig(cfg.CloudflaredConfig())
	if err != nil || conf.Domain == "" {
		return cfg.PublicURL, ""
	}
	fleetHost = "fleet." + conf.Domain
	publicURL = "https://" + fleetHost
	if cfg.PublicURL != "" {
		publicURL = cfg.PublicURL
	}
	return publicURL, fleetHost
}

func watchEdge(ctx context.Context, cfg *config.Config, api *httpapi.Server, log *slog.Logger) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	last := ""
	apply := func() {
		publicURL, host := edgeFromConfig(cfg)
		if host == last {
			return
		}
		last = host
		api.SetEdge(publicURL, host)
		if host != "" {
			log.Info("edge configured", "public", publicURL, "fleetHost", host)
		}
	}
	apply()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			apply()
		}
	}
}

// publishAssets copies the freshly built agent and bootstrap script next to
// the executable into the data dir so /download/* can serve them.
func publishAssets(cfg *config.Config, log *slog.Logger) {
	if err := os.MkdirAll(cfg.BinDir(), 0o755); err != nil {
		log.Warn("create bin dir", "err", err)
		return
	}
	exe, _ := os.Executable()
	exeDir := ""
	if exe != "" {
		exeDir = filepath.Dir(exe)
	}

	for _, agentName := range []string{
		"phleet-agent-linux-arm64",
		"phleet-agent-linux-amd64",
	} {
		agentTarget := filepath.Join(cfg.BinDir(), agentName)
		candidates := []string{
			filepath.Join(exeDir, "bin", agentName),
			filepath.Join(exeDir, "dist", "bin", agentName),
			filepath.Join("dist", "bin", agentName),
		}
		if copyIfChanged(agentTarget, candidates) {
			log.Info("published agent binary", "path", agentTarget)
		}
	}

	for _, sc := range []struct{ name, source string }{
		{"bootstrap.sh", "bootstrap-termux.sh"},
		{"bootstrap-root.sh", "bootstrap-android-root.sh"},
		{"bootstrap-debian.sh", "bootstrap-debian-chroot.sh"},
	} {
		scriptTarget := filepath.Join(cfg.BinDir(), sc.name)
		scriptCandidates := []string{
			filepath.Join(exeDir, "scripts", sc.source),
			filepath.Join(exeDir, "share", sc.name),
			filepath.Join(exeDir, "bin", sc.name),
			filepath.Join("scripts", sc.source),
		}
		if copyIfChanged(scriptTarget, scriptCandidates) {
			_ = os.Chmod(scriptTarget, 0o755)
			log.Info("published bootstrap script", "path", scriptTarget)
		}
	}
}

// copyIfChanged refreshes the published asset when the build changed. The old
// behaviour only copied when the target was missing, so upgrading phleetd left
// phones downloading the previous agent and bootstrap forever.
func copyIfChanged(target string, candidates []string) bool {
	for _, src := range candidates {
		if sameFile(target, src) {
			return false
		}
		in, err := os.Open(src)
		if err != nil {
			continue
		}
		out, err := os.Create(target)
		if err != nil {
			in.Close()
			continue
		}
		_, copyErr := io.Copy(out, in)
		out.Close()
		in.Close()
		if copyErr == nil {
			return true
		}
	}
	return false
}

func sameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil || sa.IsDir() || sb.IsDir() || sa.Size() != sb.Size() {
		return false
	}
	ba, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	bb, err := os.ReadFile(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ba, bb)
}
