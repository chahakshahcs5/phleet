package engines

import (
	"fmt"
	"strconv"
)

// Additional addon engines. The original catalogue covered databases, caches
// and object storage; these cover the other things a phone can usefully run:
// messaging, search-adjacent tooling, metrics, logs and notifications.

func init() {
	registry["memcached"] = &Engine{
		ID:          "memcached",
		Name:        "Memcached",
		Kind:        KindCache,
		Summary:     "Tiny in-memory cache for sessions, rate limits and hot keys. No persistence.",
		Package:     "memcached",
		AptPackage:  "memcached",
		Binaries:    []string{"memcached"},
		MinRAMMB:    16,
		Console:     false,
		NoBackup:    true,
		Replication: "none",
		DefaultPort: 11211,
		StopSignal:  "TERM",
		StopTimeout: 15,
		Init:        func(Params) string { return "" },
		Run:         memcachedRun,
		Health:      func(Params) string { return "" },
		PostStart:   func(Params) string { return "" },
		Env:         memcachedEnv,
	}

	registry["mosquitto"] = &Engine{
		ID:          "mosquitto",
		Name:        "Mosquitto",
		Kind:        KindQueue,
		Summary:     "MQTT broker for IoT devices and phone-to-phone messaging, with a persistent message store.",
		Package:     "mosquitto",
		AptPackage:  "mosquitto",
		Binaries:    []string{"mosquitto"},
		MinRAMMB:    16,
		Console:     false,
		Replication: "none",
		DefaultPort: 1883,
		StopSignal:  "TERM",
		StopTimeout: 20,
		RestoreStop: true,
		Init:        mosquittoInit,
		Run:         mosquittoRun,
		Health:      func(Params) string { return "" },
		PostStart:   func(Params) string { return "" },
		Backup:      tarBackup,
		Restore:     tarRestore,
		Env:         mosquittoEnv,
	}

	registry["nats"] = &Engine{
		ID:            "nats",
		Name:          "NATS",
		Kind:          KindQueue,
		Summary:       "Lightweight messaging and JetStream streams for event-driven apps.",
		Package:       "nats-server",
		AptPackage:    "nats-server",
		Binaries:      []string{"nats-server"},
		MinRAMMB:      32,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   4222,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          natsInit,
		Run:           natsRun,
		Health:        func(Params) string { return "" },
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           natsEnv,
		Install: staticInstall(staticBinary{
			name:    "nats-server",
			arm64:   "https://github.com/nats-io/nats-server/releases/download/v2.15.2/nats-server-v2.15.2-linux-arm64.tar.gz",
			amd64:   "https://github.com/nats-io/nats-server/releases/download/v2.15.2/nats-server-v2.15.2-linux-amd64.tar.gz",
			pattern: "nats-server",
		}),
	}

	registry["victoriametrics"] = &Engine{
		ID:            "victoriametrics",
		Name:          "VictoriaMetrics",
		Kind:          KindMonitoring,
		Summary:       "Fast, low-memory time-series store for metrics and monitoring. Prometheus-compatible.",
		Package:       "victoria-metrics",
		AptPackage:    "victoria-metrics",
		Binaries:      []string{"victoria-metrics"},
		MinRAMMB:      128,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   8428,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          func(p Params) string { return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data")) },
		Run:           vmRun,
		Health:        httpHealth("/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           vmEnv,
		Install: staticInstall(staticBinary{
			name:    "victoria-metrics",
			arm64:   "https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v1.155.0/victoria-metrics-linux-arm64-v1.155.0.tar.gz",
			amd64:   "https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v1.155.0/victoria-metrics-linux-amd64-v1.155.0.tar.gz",
			pattern: "victoria-metrics-prod",
		}),
	}

	registry["victorialogs"] = &Engine{
		ID:            "victorialogs",
		Name:          "VictoriaLogs",
		Kind:          KindLogging,
		Summary:       "Low-memory log store with a LogsQL query API. Collect logs from projects and phones.",
		Package:       "victoria-logs",
		AptPackage:    "victoria-logs",
		Binaries:      []string{"victoria-logs"},
		MinRAMMB:      64,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   9428,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          func(p Params) string { return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data")) },
		Run:           vlRun,
		Health:        httpHealth("/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           vlEnv,
		Install: staticInstall(staticBinary{
			name:    "victoria-logs",
			arm64:   "https://github.com/VictoriaMetrics/VictoriaLogs/releases/download/v1.53.0/victoria-logs-linux-arm64-v1.53.0.tar.gz",
			amd64:   "https://github.com/VictoriaMetrics/VictoriaLogs/releases/download/v1.53.0/victoria-logs-linux-amd64-v1.53.0.tar.gz",
			pattern: "victoria-logs-prod",
		}),
	}

	registry["pocketbase"] = &Engine{
		ID:            "pocketbase",
		Name:          "PocketBase",
		Kind:          KindBackend,
		Summary:       "Single-file backend with SQLite, auth, realtime subscriptions and an admin UI.",
		Binaries:      []string{"pocketbase"},
		MinRAMMB:      32,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   8090,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          pocketbaseInit,
		Run:           pocketbaseRun,
		Health:        httpHealth("/api/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           pocketbaseEnv,
		Install: staticInstall(staticBinary{
			name:    "pocketbase",
			arm64:   "https://github.com/pocketbase/pocketbase/releases/download/v0.40.6/pocketbase_0.40.6_linux_arm64.zip",
			amd64:   "https://github.com/pocketbase/pocketbase/releases/download/v0.40.6/pocketbase_0.40.6_linux_amd64.zip",
			pattern: "pocketbase",
			zip:     true,
		}),
	}

	registry["duckdb"] = &Engine{
		ID:            "duckdb",
		Name:          "DuckDB",
		Kind:          KindAnalytics,
		Summary:       "Embedded analytics database. Query Parquet, CSV and JSON with SQL; no server process.",
		Binaries:      []string{"duckdb"},
		MinRAMMB:      0,
		Embedded:      true,
		CustomInstall: true,
		Console:       true,
		Replication:   "none",
		DefaultPort:   0,
		StopSignal:    "TERM",
		StopTimeout:   5,
		RestoreStop:   true,
		Init:          duckdbInit,
		Run:           func(Params) string { return "" },
		Health:        duckdbHealth,
		PostStart:     func(Params) string { return "" },
		Backup:        duckdbBackup,
		Restore:       duckdbRestore,
		Env:           duckdbEnv,
		Client:        duckdbClient,
		Install: staticInstall(staticBinary{
			name:    "duckdb",
			arm64:   "https://github.com/duckdb/duckdb/releases/download/v2.0.0/duckdb-cli-linux-arm64-musl.tar.gz",
			amd64:   "https://github.com/duckdb/duckdb/releases/download/v2.0.0/duckdb-cli-linux-amd64-musl.tar.gz",
			pattern: "duckdb",
		}),
	}

	registry["ntfy"] = &Engine{
		ID:            "ntfy",
		Name:          "ntfy",
		Kind:          KindNotifications,
		Summary:       "Self-hosted push notifications over HTTP. Send alerts from projects to your phone.",
		Package:       "ntfy",
		AptPackage:    "ntfy",
		Binaries:      []string{"ntfy"},
		MinRAMMB:      32,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   2586,
		StopSignal:    "TERM",
		StopTimeout:   20,
		RestoreStop:   true,
		Init:          ntfyInit,
		Run:           ntfyRun,
		Health:        httpHealth("/v1/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           ntfyEnv,
		Install: staticInstall(staticBinary{
			name:    "ntfy",
			arm64:   "https://github.com/binwiederhier/ntfy/releases/download/v2.37.1/ntfy_2.37.1_linux_arm64.tar.gz",
			amd64:   "https://github.com/binwiederhier/ntfy/releases/download/v2.37.1/ntfy_2.37.1_linux_amd64.tar.gz",
			pattern: "ntfy",
		}),
	}

	registry["etcd"] = &Engine{
		ID:            "etcd",
		Name:          "etcd",
		Kind:          KindCoordination,
		Summary:       "Distributed key-value store for service discovery, locks and configuration. One per phone (peer port 2380).",
		Package:       "etcd-server",
		AptPackage:    "etcd-server",
		Binaries:      []string{"etcd"},
		MinRAMMB:      64,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   2379,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          etcdInit,
		Run:           etcdRun,
		Health:        httpHealth("/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           etcdEnv,
		Install: staticInstall(staticBinary{
			name:    "etcd",
			arm64:   "https://github.com/etcd-io/etcd/releases/download/v3.7.3/etcd-v3.7.3-linux-arm64.tar.gz",
			amd64:   "https://github.com/etcd-io/etcd/releases/download/v3.7.3/etcd-v3.7.3-linux-amd64.tar.gz",
			pattern: "etcd",
		}),
	}

	registry["meilisearch"] = &Engine{
		ID:            "meilisearch",
		Name:          "Meilisearch",
		Kind:          KindSearch,
		Summary:       "Fast full-text search with typo tolerance. Large binary (~350 MB); ~128 MB RAM.",
		Binaries:      []string{"meilisearch"},
		MinRAMMB:      128,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   7700,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          meiliInit,
		Run:           meiliRun,
		Health:        httpHealth("/health"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           meiliEnv,
		Install: staticInstall(staticBinary{
			name:  "meilisearch",
			arm64: "https://github.com/meilisearch/meilisearch/releases/download/v1.56.1/meilisearch-linux-aarch64",
			amd64: "https://github.com/meilisearch/meilisearch/releases/download/v1.56.1/meilisearch-linux-amd64",
			raw:   true,
		}),
	}

	registry["prometheus"] = &Engine{
		ID:            "prometheus",
		Name:          "Prometheus",
		Kind:          KindMonitoring,
		Summary:       "Metrics scraper and time-series database with a query API. ~256 MB RAM.",
		Package:       "prometheus",
		AptPackage:    "prometheus",
		Binaries:      []string{"prometheus"},
		MinRAMMB:      256,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   9090,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          prometheusInit,
		Run:           prometheusRun,
		Health:        httpHealth("/-/healthy"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           prometheusEnv,
		Install: staticInstall(staticBinary{
			name:    "prometheus",
			arm64:   "https://github.com/prometheus/prometheus/releases/download/v3.15.1/prometheus-3.15.1.linux-arm64.tar.gz",
			amd64:   "https://github.com/prometheus/prometheus/releases/download/v3.15.1/prometheus-3.15.1.linux-amd64.tar.gz",
			pattern: "prometheus",
		}),
	}

	registry["gitea"] = &Engine{
		ID:            "gitea",
		Name:          "Gitea",
		Kind:          KindGit,
		Summary:       "Self-hosted Git service with issues, pull requests and a web UI. SQLite-backed.",
		Binaries:      []string{"gitea"},
		MinRAMMB:      128,
		CustomInstall: true,
		Console:       false,
		Replication:   "none",
		DefaultPort:   3000,
		StopSignal:    "TERM",
		StopTimeout:   30,
		RestoreStop:   true,
		Init:          giteaInit,
		Run:           giteaRun,
		Health:        httpHealth("/api/healthz"),
		PostStart:     func(Params) string { return "" },
		Backup:        tarBackup,
		Restore:       tarRestore,
		Env:           giteaEnv,
		Install: staticInstall(staticBinary{
			name:  "gitea",
			arm64: "https://github.com/go-gitea/gitea/releases/download/v28.3.0/gitea-28.3.0-linux-arm64",
			amd64: "https://github.com/go-gitea/gitea/releases/download/v28.3.0/gitea-28.3.0-linux-amd64",
			raw:   true,
		}),
	}

	registry["beanstalkd"] = &Engine{
		ID:          "beanstalkd",
		Name:        "Beanstalkd",
		Kind:        KindQueue,
		Summary:     "Simple, fast work queue with a persistent binlog. Debian package only.",
		AptPackage:  "beanstalkd",
		Binaries:    []string{"beanstalkd"},
		MinRAMMB:    16,
		Platforms:   []string{"apt"},
		Console:     false,
		Replication: "none",
		DefaultPort: 11300,
		StopSignal:  "TERM",
		StopTimeout: 20,
		RestoreStop: true,
		Init:        beanstalkdInit,
		Run:         beanstalkdRun,
		Health:      func(Params) string { return "" },
		PostStart:   func(Params) string { return "" },
		Backup:      tarBackup,
		Restore:     tarRestore,
		Env:         beanstalkdEnv,
	}

}

// staticBinary describes a release tarball whose single binary is copied into
// $PHLEET_STATE_DIR/bin. It is used on phones without a package for the engine
// (typically Termux); Debian installs the same engine with apt.
type staticBinary struct {
	name    string
	arm64   string
	amd64   string
	pattern string
	zip     bool
	raw     bool // the URL is the binary itself, not an archive
}

func staticInstall(s staticBinary) func(Params) string {
	extract := fmt.Sprintf("tar -xzf archive\nSRC=\"$(find . -type f -name %s | head -n 1)\"", shq(s.pattern))
	if s.zip {
		extract = fmt.Sprintf(`if ! command -v unzip >/dev/null 2>&1 && command -v pkg >/dev/null 2>&1; then
  pkg install -y unzip >/dev/null 2>&1 || true
fi
command -v unzip >/dev/null 2>&1 || { echo "unzip is required to install %s"; exit 1; }
unzip -q archive
SRC="$(find . -type f -name %s | head -n 1)"`, s.name, shq(s.pattern))
	}
	if s.raw {
		extract = "SRC=archive"
	}
	return func(Params) string {
		return fmt.Sprintf(`set -eu
BIN="${PHLEET_STATE_DIR:-$HOME/.phleet}/bin"
if command -v %[1]s >/dev/null 2>&1; then exit 0; fi
case "$(uname -m)" in
  aarch64|arm64) URL=%[2]s ;;
  x86_64|amd64) URL=%[3]s ;;
  *) echo "unsupported architecture for %[1]s"; exit 1 ;;
esac
mkdir -p "$BIN"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM
cd "$TMP"
curl -fsSL "$URL" -o archive
%[4]s
[ -n "$SRC" ] || { echo "%[1]s binary not found in archive"; exit 1; }
cp "$SRC" "$BIN/%[1]s.tmp"
chmod 755 "$BIN/%[1]s.tmp"
mv -f "$BIN/%[1]s.tmp" "$BIN/%[1]s"
`, s.name, shq(s.arm64), shq(s.amd64), extract)
	}
}

func httpHealth(path string) func(Params) string {
	return func(p Params) string {
		return fmt.Sprintf("curl -fsS -m 3 -o /dev/null http://127.0.0.1:%d%s", port(p), path)
	}
}

// tarBackup/tarRestore are the common case for services whose state is a data
// directory. The .phleet-addon marker is excluded from the archive and kept in
// place on restore so the provisioner still recognises the directory.
func tarBackup(p Params, dest string) string {
	return fmt.Sprintf(`set -eu
tar -czf %s -C %s --exclude='./backups' --exclude='./.phleet-addon' .
`, shq(dest), shq(p.DataDir))
}

func tarRestore(p Params, src string) string {
	return fmt.Sprintf(`set -eu
rm -rf %s/*
tar -xzf %s -C %s
`, shq(p.DataDir), shq(src), shq(p.DataDir))
}

// ---- Memcached ----

func memcachedRun(p Params) string {
	flags := fmt.Sprintf("-l 127.0.0.1 -p %d -m 64 -c 1024", port(p))
	// memcached refuses to start as root unless it is told which user to drop
	// to; inside a root-owned container it would otherwise exit immediately.
	return fmt.Sprintf(`if [ "$(id -u)" = "0" ]; then exec memcached -u root %s; else exec memcached %s; fi`, flags, flags)
}

func memcachedEnv(p Params) map[string]string {
	addr := fmt.Sprintf("127.0.0.1:%d", port(p))
	return map[string]string{
		"MEMCACHED_HOST":    "127.0.0.1",
		"MEMCACHED_PORT":    strconv.Itoa(port(p)),
		"MEMCACHED_SERVERS": addr,
	}
}

// ---- Mosquitto ----

func mosquittoInit(p Params) string {
	conf := fmt.Sprintf(`listener %d 127.0.0.1
allow_anonymous true
persistence true
persistence_location %s/
autosave_interval 60
log_dest stdout
`, port(p), p.DataDir)
	return fmt.Sprintf("cat > %s <<'PHLEET_MOSQUITTO'\n%sPHLEET_MOSQUITTO\n", shq(p.DataDir+"/mosquitto.conf"), conf)
}

func mosquittoRun(p Params) string {
	return fmt.Sprintf("exec mosquitto -c %s", shq(p.DataDir+"/mosquitto.conf"))
}

func mosquittoEnv(p Params) map[string]string {
	return map[string]string{
		"MQTT_HOST": "127.0.0.1",
		"MQTT_PORT": strconv.Itoa(port(p)),
		"MQTT_URL":  fmt.Sprintf("mqtt://127.0.0.1:%d", port(p)),
	}
}

// ---- NATS ----

func natsInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/jetstream"))
}

func natsRun(p Params) string {
	return fmt.Sprintf("exec nats-server -a 127.0.0.1 -p %d -js -sd %s", port(p), shq(p.DataDir+"/jetstream"))
}

func natsEnv(p Params) map[string]string {
	return map[string]string{
		"NATS_HOST": "127.0.0.1",
		"NATS_PORT": strconv.Itoa(port(p)),
		"NATS_URL":  fmt.Sprintf("nats://127.0.0.1:%d", port(p)),
	}
}

// ---- VictoriaMetrics ----

func vmRun(p Params) string {
	return fmt.Sprintf("exec victoria-metrics -storageDataPath %s -httpListenAddr 127.0.0.1:%d -retentionPeriod 30d",
		shq(p.DataDir+"/data"), port(p))
}

func vmEnv(p Params) map[string]string {
	base := fmt.Sprintf("http://127.0.0.1:%d", port(p))
	return map[string]string{
		"VICTORIAMETRICS_URL": base,
		"VM_URL":              base,
		"VM_PUSH_URL":         base + "/api/v1/write",
	}
}

// ---- VictoriaLogs ----

func vlRun(p Params) string {
	return fmt.Sprintf("exec victoria-logs -storageDataPath %s -httpListenAddr 127.0.0.1:%d -retentionPeriod 30d",
		shq(p.DataDir+"/data"), port(p))
}

func vlEnv(p Params) map[string]string {
	base := fmt.Sprintf("http://127.0.0.1:%d", port(p))
	return map[string]string{
		"VICTORIALOGS_URL": base,
		"VL_URL":           base,
		"VL_INSERT_URL":    base + "/insert/jsonline",
	}
}

// ---- ntfy ----

func ntfyInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/attachments"))
}

func ntfyRun(p Params) string {
	return fmt.Sprintf("exec ntfy serve --listen-http 127.0.0.1:%d --cache-file %s --attachment-cache-dir %s --base-url http://127.0.0.1:%d",
		port(p), shq(p.DataDir+"/cache.db"), shq(p.DataDir+"/attachments"), port(p))
}

func ntfyEnv(p Params) map[string]string {
	return map[string]string{
		"NTFY_URL": fmt.Sprintf("http://127.0.0.1:%d", port(p)),
	}
}

// ---- etcd ----

func etcdInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data"))
}

func etcdRun(p Params) string {
	// One etcd per phone. The client port is allocated; the peer port is fixed
	// because the addon model allocates one port per addon.
	const peer = 2380
	return fmt.Sprintf("exec etcd --name default --data-dir %s --listen-client-urls http://127.0.0.1:%d --advertise-client-urls http://127.0.0.1:%d --listen-peer-urls http://127.0.0.1:%d --initial-advertise-peer-urls http://127.0.0.1:%d --initial-cluster default=http://127.0.0.1:%d",
		shq(p.DataDir+"/data"), port(p), port(p), peer, peer, peer)
}

func etcdEnv(p Params) map[string]string {
	base := fmt.Sprintf("http://127.0.0.1:%d", port(p))
	return map[string]string{
		"ETCD_ENDPOINTS":    base,
		"ETCDCTL_ENDPOINTS": base,
	}
}

// ---- Meilisearch ----

func meiliInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/data.ms"))
}

func meiliRun(p Params) string {
	return fmt.Sprintf("exec meilisearch --http-addr 127.0.0.1:%d --db-path %s --env production --no-analytics --master-key %s",
		port(p), shq(p.DataDir+"/data.ms"), shq(p.Password))
}

func meiliEnv(p Params) map[string]string {
	return map[string]string{
		"MEILISEARCH_URL":  fmt.Sprintf("http://127.0.0.1:%d", port(p)),
		"MEILI_MASTER_KEY": p.Password,
		"MEILI_HTTP_ADDR":  fmt.Sprintf("127.0.0.1:%d", port(p)),
	}
}

// ---- Prometheus ----

func prometheusInit(p Params) string {
	conf := "global:\n  scrape_interval: 30s\nscrape_configs: []\n"
	return fmt.Sprintf("mkdir -p %s\ncat > %s <<'PHLEET_PROM'\n%sPHLEET_PROM\n",
		shq(p.DataDir+"/data"), shq(p.DataDir+"/prometheus.yml"), conf)
}

func prometheusRun(p Params) string {
	return fmt.Sprintf("exec prometheus --config.file=%s --storage.tsdb.path=%s --web.listen-address=127.0.0.1:%d --web.enable-lifecycle",
		shq(p.DataDir+"/prometheus.yml"), shq(p.DataDir+"/data"), port(p))
}

func prometheusEnv(p Params) map[string]string {
	return map[string]string{
		"PROMETHEUS_URL": fmt.Sprintf("http://127.0.0.1:%d", port(p)),
	}
}

// ---- Gitea ----

func giteaInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s %s %s\n",
		shq(p.DataDir+"/data"), shq(p.DataDir+"/repos"), shq(p.DataDir+"/logs"))
}

func giteaRun(p Params) string {
	conf := p.DataDir + "/app.ini"
	// The heredoc delimiter is intentionally unquoted so $(id -un) expands to
	// the user the agent runs as; Gitea refuses to start when RUN_USER does not
	// match the current user.
	return fmt.Sprintf(`cat > %[1]s <<PHLEET_GITEA
APP_NAME = phleet Gitea
RUN_USER = $(id -un)
WORK_PATH = %[2]s
[server]
HTTP_ADDR = 127.0.0.1
HTTP_PORT = %[3]d
ROOT_URL = http://127.0.0.1:%[3]d/
DISABLE_SSH = true
[database]
DB_TYPE = sqlite3
PATH = %[2]s/data/gitea.db
[repository]
ROOT = %[2]s/repos
[log]
ROOT_PATH = %[2]s/logs
PHLEET_GITEA
exec gitea web --config %[1]s`, shq(conf), p.DataDir, port(p))
}

func giteaEnv(p Params) map[string]string {
	return map[string]string{
		"GITEA_URL": fmt.Sprintf("http://127.0.0.1:%d", port(p)),
	}
}

// ---- Beanstalkd ----

func beanstalkdInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/binlog"))
}

func beanstalkdRun(p Params) string {
	return fmt.Sprintf("exec beanstalkd -l 127.0.0.1 -p %d -b %s", port(p), shq(p.DataDir+"/binlog"))
}

func beanstalkdEnv(p Params) map[string]string {
	return map[string]string{
		"BEANSTALKD_URL": fmt.Sprintf("beanstalkd://127.0.0.1:%d", port(p)),
	}
}

// ---- Uptime Kuma ----

// ---- PocketBase ----

func pocketbaseInit(p Params) string {
	return fmt.Sprintf("mkdir -p %s\n", shq(p.DataDir+"/pb_data"))
}

func pocketbaseRun(p Params) string {
	return fmt.Sprintf("exec pocketbase serve --http 127.0.0.1:%d --dir %s", port(p), shq(p.DataDir+"/pb_data"))
}

func pocketbaseEnv(p Params) map[string]string {
	return map[string]string{
		"POCKETBASE_URL": fmt.Sprintf("http://127.0.0.1:%d", port(p)),
	}
}

// ---- DuckDB ----

func duckdbInit(p Params) string {
	return fmt.Sprintf("duckdb %s -c 'CREATE TABLE IF NOT EXISTS _phleet_health(x INTEGER);'\n", shq(p.File))
}

func duckdbHealth(p Params) string {
	return fmt.Sprintf("test -f %s", shq(p.File))
}

func duckdbBackup(p Params, dest string) string {
	return fmt.Sprintf("cp %s %s\n", shq(p.File), shq(dest))
}

func duckdbRestore(p Params, src string) string {
	return fmt.Sprintf("cp %s %s\n", shq(src), shq(p.File))
}

func duckdbEnv(p Params) map[string]string {
	return map[string]string{"DUCKDB_PATH": p.File}
}

func duckdbClient(p Params) string {
	return "duckdb " + shq(p.File)
}
