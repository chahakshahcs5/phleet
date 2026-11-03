package engines

import "fmt"

// Self-hosted applications. Unlike addons, a service is the app itself: a
// prebuilt release installed from a catalogue, exposed on its own hostname, and
// supervised exactly like a project. It has no source/build/deploy step.
func init() {
	registry["filebrowser"] = &Engine{
		ID:            "filebrowser",
		Name:          "Filebrowser",
		Kind:          KindService,
		Summary:       "Web file manager with uploads, sharing and a built-in editor. Files live under the service data directory.",
		Service:       true,
		Category:      "files",
		Website:       "https://filebrowser.org",
		Source:        "https://github.com/filebrowser/filebrowser",
		Icon:          "folder",
		Version:       "v2.63.23",
		Ingress:       true,
		Binaries:      []string{"filebrowser"},
		MinRAMMB:      32,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   8080,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          filebrowserInit,
		Run:           filebrowserRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("FILEBROWSER_URL"),
		Install: staticInstall(staticBinary{
			name:    "filebrowser",
			arm64:   "https://github.com/filebrowser/filebrowser/releases/download/v2.63.23/linux-arm64-filebrowser.tar.gz",
			amd64:   "https://github.com/filebrowser/filebrowser/releases/download/v2.63.23/linux-amd64-filebrowser.tar.gz",
			pattern: "filebrowser",
		}),
	}

	registry["memos"] = &Engine{
		ID:            "memos",
		Name:          "Memos",
		Kind:          KindService,
		Summary:       "Lightweight, self-hosted note-taking and knowledge base with tags and Markdown.",
		Service:       true,
		Category:      "notes",
		Website:       "https://usememos.com",
		Source:        "https://github.com/usememos/memos",
		Icon:          "notebook",
		Version:       "26.11.0",
		Ingress:       true,
		Binaries:      []string{"memos"},
		MinRAMMB:      32,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   5230,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          memosInit,
		Run:           memosRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("MEMOS_URL"),
		Install: staticInstall(staticBinary{
			name:    "memos",
			arm64:   "https://github.com/usememos/memos/releases/download/26.11.0/memos_26.11.0_linux_arm64.tar.gz",
			amd64:   "https://github.com/usememos/memos/releases/download/26.11.0/memos_26.11.0_linux_amd64.tar.gz",
			pattern: "memos",
		}),
	}

	registry["gotify"] = &Engine{
		ID:            "gotify",
		Name:          "Gotify",
		Kind:          KindService,
		Summary:       "Self-hosted push notification server with a web UI and a simple HTTP API.",
		Service:       true,
		Category:      "notifications",
		Website:       "https://gotify.net",
		Source:        "https://github.com/gotify/server",
		Icon:          "bell",
		Version:       "v3.1.1",
		Ingress:       true,
		Binaries:      []string{"gotify"},
		MinRAMMB:      32,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   8080,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          gotifyInit,
		Run:           gotifyRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("GOTIFY_URL"),
		Install: staticInstall(staticBinary{
			name:    "gotify",
			arm64:   "https://github.com/gotify/server/releases/download/v3.1.1/gotify-linux-arm64.zip",
			amd64:   "https://github.com/gotify/server/releases/download/v3.1.1/gotify-linux-amd64.zip",
			pattern: "gotify*",
			zip:     true,
		}),
	}

	registry["microbin"] = &Engine{
		ID:            "microbin",
		Name:          "MicroBin",
		Kind:          KindService,
		Summary:       "Tiny pastebin and file-sharing service with expiring pastes and QR codes.",
		Service:       true,
		Category:      "utilities",
		Website:       "https://microbin.eu",
		Source:        "https://github.com/szabodanika/microbin",
		Icon:          "clipboard",
		Version:       "v2.1.0",
		Ingress:       true,
		Binaries:      []string{"microbin"},
		MinRAMMB:      16,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   8080,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          microbinInit,
		Run:           microbinRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("MICROBIN_URL"),
		Install: staticInstall(staticBinary{
			name:    "microbin",
			arm64:   "https://github.com/szabodanika/microbin/releases/download/v2.1.0/microbin-v2.1.0-aarch64-unknown-linux-musl.tar.gz",
			amd64:   "https://github.com/szabodanika/microbin/releases/download/v2.1.0/microbin-v2.1.0-x86_64-unknown-linux-musl.tar.gz",
			pattern: "microbin",
		}),
	}

	registry["navidrome"] = &Engine{
		ID:            "navidrome",
		Name:          "Navidrome",
		Kind:          KindService,
		Summary:       "Music server and streamer with a Subsonic-compatible API. Put music in the service's music folder.",
		Service:       true,
		Category:      "media",
		Website:       "https://www.navidrome.org",
		Source:        "https://github.com/navidrome/navidrome",
		Icon:          "music",
		Version:       "v0.64.2",
		Ingress:       true,
		Binaries:      []string{"navidrome"},
		MinRAMMB:      64,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   4533,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          navidromeInit,
		Run:           navidromeRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("NAVIDROME_URL"),
		Install: staticInstall(staticBinary{
			name:    "navidrome",
			arm64:   "https://github.com/navidrome/navidrome/releases/download/v0.64.2/navidrome_0.64.2_linux_arm64.tar.gz",
			amd64:   "https://github.com/navidrome/navidrome/releases/download/v0.64.2/navidrome_0.64.2_linux_amd64.tar.gz",
			pattern: "navidrome",
		}),
	}

	registry["vikunja"] = &Engine{
		ID:            "vikunja",
		Name:          "Vikunja",
		Kind:          KindService,
		Summary:       "Self-hosted to-do and project management with lists, kanban, teams and reminders.",
		Service:       true,
		Category:      "productivity",
		Website:       "https://vikunja.io",
		Source:        "https://github.com/go-vikunja/vikunja",
		Icon:          "check",
		Version:       "v2.7.1",
		Ingress:       true,
		Binaries:      []string{"vikunja"},
		MinRAMMB:      64,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   3456,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          vikunjaInit,
		Run:           vikunjaRun,
		Health:        httpHealth("/api/v1/info"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("VIKUNJA_URL"),
		Install: treeInstall(treeBinary{
			name:    "vikunja",
			arm64:   "https://github.com/go-vikunja/vikunja/releases/download/v2.7.1/vikunja-v2.7.1-linux-arm64-full.zip",
			amd64:   "https://github.com/go-vikunja/vikunja/releases/download/v2.7.1/vikunja-v2.7.1-linux-amd64-full.zip",
			pattern: "vikunja*",
			zip:     true,
		}),
	}

	registry["miniflux"] = &Engine{
		ID:            "miniflux",
		Name:          "Miniflux",
		Kind:          KindService,
		Summary:       "Minimalist RSS reader. Needs a PostgreSQL addon; paste its DATABASE_URL when installing.",
		Service:       true,
		Category:      "reading",
		Website:       "https://miniflux.app",
		Source:        "https://github.com/miniflux/v2",
		Icon:          "rss",
		Version:       "2.3.5",
		Ingress:       true,
		Binaries:      []string{"miniflux"},
		MinRAMMB:      32,
		CustomInstall: true,
		NoBackup:      true,
		Replication:   "none",
		DefaultPort:   8080,
		StopSignal:    "TERM",
		StopTimeout:   20,
		Requires:      []string{"postgres"},
		Init:          func(Params) string { return "" },
		Run:           minifluxRun,
		Health:        httpHealth("/healthz"),
		PostStart:     func(Params) string { return "" },
		Env:           serviceEnv("MINIFLUX_URL"),
		Install: staticInstall(staticBinary{
			name:  "miniflux",
			arm64: "https://github.com/miniflux/v2/releases/download/2.3.5/miniflux-linux-arm64",
			amd64: "https://github.com/miniflux/v2/releases/download/2.3.5/miniflux-linux-amd64",
			raw:   true,
		}),
	}

	registry["syncthing"] = &Engine{
		ID:            "syncthing",
		Name:          "Syncthing",
		Kind:          KindService,
		Summary:       "Continuous file synchronisation between phones and computers, with a web UI.",
		Service:       true,
		Category:      "files",
		Website:       "https://syncthing.net",
		Source:        "https://github.com/syncthing/syncthing",
		Icon:          "refresh",
		Version:       "v2.1.7",
		Ingress:       true,
		Binaries:      []string{"syncthing"},
		MinRAMMB:      64,
		CustomInstall: true,
		Replication:   "none",
		DefaultPort:   8384,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          syncthingInit,
		Run:           syncthingRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           serviceEnv("SYNCTHING_URL"),
		Install: staticInstall(staticBinary{
			name:    "syncthing",
			arm64:   "https://github.com/syncthing/syncthing/releases/download/v2.1.7/syncthing-linux-arm64-v2.1.7.tar.gz",
			amd64:   "https://github.com/syncthing/syncthing/releases/download/v2.1.7/syncthing-linux-amd64-v2.1.7.tar.gz",
			pattern: "syncthing",
		}),
	}
}

// treeBinary installs a whole release tree (binary + frontend assets) under
// $PHLEET_STATE_DIR/services/<name> and leaves a small wrapper in bin/. This is
// for apps like Vikunja whose web UI is not embedded in the binary.
type treeBinary struct {
	name    string
	arm64   string
	amd64   string
	pattern string
	zip     bool
}

func treeInstall(t treeBinary) func(Params) string {
	extract := fmt.Sprintf("tar -xzf archive\ncp -a ./* \"$TREE\"/ 2>/dev/null || true")
	if t.zip {
		extract = fmt.Sprintf(`if ! command -v unzip >/dev/null 2>&1 && command -v pkg >/dev/null 2>&1; then
  pkg install -y unzip >/dev/null 2>&1 || true
fi
command -v unzip >/dev/null 2>&1 || { echo "unzip is required to install %s"; exit 1; }
unzip -q archive
cp -a ./* "$TREE"/ 2>/dev/null || true`, t.name)
	}
	return func(Params) string {
		return fmt.Sprintf(`set -eu
BIN="${PHLEET_STATE_DIR:-$HOME/.phleet}/bin"
TREE="${PHLEET_STATE_DIR:-$HOME/.phleet}/services/%[1]s"
find_src() {
  find "$TREE" -type f -name %[5]s ! -name '*.sha256' ! -name '*.asc' ! -name '*.sigstore.json' | head -n 1
}
write_wrapper() {
  chmod 755 "$1"
  cat > "$BIN/%[1]s" <<EOF
#!/bin/sh
cd "$(dirname "$1")" || exit 1
exec "$1" "\$@"
EOF
  chmod 755 "$BIN/%[1]s"
}
if [ -x "$BIN/%[1]s" ] && [ -d "$TREE" ]; then exit 0; fi
SRC="$(find_src)"
if [ -n "$SRC" ]; then write_wrapper "$SRC"; exit 0; fi
case "$(uname -m)" in
  aarch64|arm64) URL=%[2]s ;;
  x86_64|amd64) URL=%[3]s ;;
  *) echo "unsupported architecture for %[1]s"; exit 1 ;;
esac
mkdir -p "$BIN" "$TREE"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM
cd "$TMP"
curl -fsSL "$URL" -o archive
%[4]s
SRC="$(find_src)"
[ -n "$SRC" ] || { echo "%[1]s binary not found in archive"; exit 1; }
write_wrapper "$SRC"
`, t.name, shq(t.arm64), shq(t.amd64), extract, shq(t.pattern))
	}
}

func serviceEnv(key string) func(Params) map[string]string {
	return func(p Params) map[string]string {
		return map[string]string{key: fmt.Sprintf("http://127.0.0.1:%d", port(p))}
	}
}

func filebrowserInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/files"))
}

func filebrowserRun(p Params) string {
	return fmt.Sprintf("exec filebrowser -a 127.0.0.1 -p %d -d %s -r %s",
		port(p), shq(p.DataDir+"/filebrowser.db"), shq(p.DataDir+"/files"))
}

func memosInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data"))
}

func memosRun(p Params) string {
	// Memos 26 removed --mode; bind explicitly to loopback.
	return fmt.Sprintf("exec memos --addr 127.0.0.1 --port %d --data %s", port(p), shq(p.DataDir+"/data"))
}

func gotifyInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data"))
}

func gotifyRun(p Params) string {
	return fmt.Sprintf("exec gotify server --listen-addr 127.0.0.1 --port %d --data-dir %s", port(p), shq(p.DataDir+"/data"))
}

func microbinInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data"))
}

func microbinRun(p Params) string {
	return fmt.Sprintf("exec microbin --port %d --data-dir %s", port(p), shq(p.DataDir+"/data"))
}

func navidromeInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s %s\n", shq(p.DataDir+"/data"), shq(p.DataDir+"/music"))
}

func navidromeRun(p Params) string {
	return fmt.Sprintf("exec navidrome --address 127.0.0.1 --port %d --datafolder %s --musicfolder %s",
		port(p), shq(p.DataDir+"/data"), shq(p.DataDir+"/music"))
}

func syncthingInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir))
}

func syncthingRun(p Params) string {
	return fmt.Sprintf("exec syncthing serve --home %s --gui-address 127.0.0.1:%d --no-browser --no-restart",
		shq(p.DataDir), port(p))
}

func vikunjaInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/files"))
}

func vikunjaRun(p Params) string {
	return fmt.Sprintf("VIKUNJA_SERVICE_INTERFACE=127.0.0.1:%d VIKUNJA_SERVICE_PUBLICURL=http://127.0.0.1:%d VIKUNJA_SERVICE_SECRET=%s VIKUNJA_DATABASE_TYPE=sqlite VIKUNJA_DATABASE_PATH=%s VIKUNJA_FILES_BASEPATH=%s VIKUNJA_CORS_ENABLE=false exec vikunja web",
		port(p), port(p), shq(p.Password), shq(p.DataDir+"/vikunja.db"), shq(p.DataDir+"/files"))
}

func minifluxRun(p Params) string {
	// DATABASE_URL comes from the service environment supplied at install time.
	return fmt.Sprintf("LISTEN_ADDR=127.0.0.1:%d BASE_URL=http://127.0.0.1:%d RUN_MIGRATIONS=1 CREATE_ADMIN=1 ADMIN_USERNAME=%s ADMIN_PASSWORD=%s exec miniflux",
		port(p), port(p), shq(p.User), shq(p.Password))
}
