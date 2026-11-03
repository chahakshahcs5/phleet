//go:build linux

// Command phleet-agent runs inside Termux and connects outbound to the
// control plane. It never listens on a port.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"phleet/internal/agent"
	"phleet/internal/config"
)

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	server := flag.String("server", env("PHLEET_URL", ""), "control plane URL, e.g. https://fleet.example.com")
	token := flag.String("token", "", "agent token (deprecated: visible in process args; prefer -token-file)")
	tokenFile := flag.String("token-file", env("PHLEET_TOKEN_FILE", ""), "file containing the agent token")
	name := flag.String("name", env("PHLEET_NAME", ""), "display name for this phone")
	id := flag.String("id", env("PHLEET_ID", ""), "stable agent id (generated on first run)")
	stateDir := flag.String("state", "", "state directory (default ~/.phleet)")
	var roots multiFlag
	flag.Var(&roots, "root", "extra directory the file browser and shell may access (repeatable)")
	showVersion := flag.Bool("version", false, "print version")
	debug := flag.Bool("debug", false, "debug logging")
	flag.Parse()

	if *showVersion {
		fmt.Println("phleet-agent " + config.Version)
		return
	}
	if *server == "" {
		fmt.Fprintln(os.Stderr, "error: --server is required (or PHLEET_URL)")
		os.Exit(2)
	}
	u, err := normalizeURL(*server)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	home, _ := os.UserHomeDir()
	if *stateDir == "" {
		*stateDir = filepath.Join(home, ".phleet")
	}
	// Prefer a token file so the secret never appears in the process command
	// line, where any process running as the same user can read it.
	if strings.TrimSpace(*token) == "" {
		file := strings.TrimSpace(*tokenFile)
		if file == "" {
			file = filepath.Join(*stateDir, "agent.token")
		}
		if b, err := os.ReadFile(file); err == nil {
			*token = strings.TrimSpace(string(b))
			*tokenFile = file
		}
	}
	// Env is a last resort: a persisted per-device token in a file must win
	// over a stale PHLEET_TOKEN exported by an old installer.
	if strings.TrimSpace(*token) == "" {
		*token = strings.TrimSpace(os.Getenv("PHLEET_TOKEN"))
	}
	if strings.TrimSpace(*token) == "" {
		fmt.Fprintln(os.Stderr, "error: an agent token is required (--token-file, --token or PHLEET_TOKEN)")
		os.Exit(2)
	}
	*token = strings.TrimSpace(*token)
	if *name == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			*name = h
		} else {
			*name = "android"
		}
	}

	a, err := agent.New(agent.Config{
		URL:       u,
		Token:     *token,
		TokenPath: *tokenFile,
		Name:      *name,
		ID:        *id,
		StateDir:  *stateDir,
		Roots:     roots,
	}, log)
	if err != nil {
		log.Error("init failed", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Info("starting", "server", u, "id", a.ID(), "name", *name, "version", config.Version)
	if err := a.Run(ctx); err != nil && err != context.Canceled {
		log.Error("stopped", "err", err)
		os.Exit(1)
	}
}

func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if !strings.HasSuffix(u.Path, "/agent") {
		u.Path = strings.TrimRight(u.Path, "/") + "/agent"
	}
	return u.String(), nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
