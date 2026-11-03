# phleet

A self-hosted PaaS for the Android phones you already own. phleet turns a drawer of old
phones running Termux into a small private cloud: deploy projects to them, run databases
next to those projects, and manage the whole fleet from one panel.

```
Internet ──▶ Cloudflare ──▶ cloudflared (Windows) ──▶ phleetd ──▶ fleet-agent (Termux) ──▶ your apps
```

Phones **never accept inbound connections**. Each agent dials out over TLS to the control plane, and every dashboard action, terminal keystroke, deploy log and HTTP request travels back over that connection. Cloudflare Tunnel publishes the panel and your projects without opening a single port.

## Why phleet

A phone you replaced two years ago still has an eight-core CPU, 4–8 GB of RAM, tens of
gigabytes of flash and a battery that doubles as a UPS. It is a capable always-on server
that happens to fit in a pocket. phleet exists to put that hardware back to work instead
of leaving it in a drawer:

- **A PaaS for hardware you already own.** Point it at a Git repository or upload a folder.
  phleet builds it on the phone, starts a supervised release, health-checks it, serves it on
  `<slug>.<domain>` and rolls back automatically when the new release is unhealthy.
- **Fleet management, not just deploys.** Battery, thermal, disk and connectivity telemetry,
  a browser terminal, a file manager and a process list for every device.
- **Data next to the app.** PostgreSQL, Redis, SQLite, S3 and fifteen other engines run on
  the same phones, with encrypted credentials, backups, relay between phones and opt-in
  failover.
- **No root, no containers, no open ports.** The agent runs as an ordinary Termux user and
  dials out; Cloudflare Tunnel publishes the panel and the apps.

The result is a personal cloud with no monthly bill and no rack: add a phone, give it a
label, and deploy to it like any other platform.

**Status:** deploy pipeline, fleet scheduling, stacks and the service catalogue are
implemented. Inventory, metrics, terminal, files, git-to-phone deploys, supervised
processes, rollback, replicas with label selectors, drain/failover, per-project subdomains,
alerts with Telegram/webhook notifications, Cloudflare automation, Termux bootstrap and
on-phone data services (PostgreSQL, Redis, SQLite and S3) with relay, PC bridge, backups and
standbys all work.

## What works today

| Area | Capability |
|---|---|
| Fleet | Auto-registration, rename, labels, online/offline with 45 s staleness, drain mode, delete |
| Onboarding | **Fleet → Add phone**: install over SSH using an existing `~/.ssh/config` alias, or copy a ready-made Termux command |
| Telemetry | CPU, memory, disk, thermal zones, battery + charging, load, network counters, uptime — 5 s heartbeats, 14-day retention, downsampled charts |
| Operations | Browser terminal (xterm.js over the agent PTY), command runner, process list |
| Files | Browse; upload files or whole folders (zipped in the browser and extracted on the phone); download; edit text files; mkdir, rename, delete — sandboxed to `$HOME` |
| Projects | Git repo **or uploaded source folder** → build → release → supervised process → health check → rollback, five releases kept |
| Services | Ready-built self-hosted apps (Filebrowser, Memos, Gotify, MicroBin, Navidrome, Syncthing) installed from a catalogue, exposed on `<slug>.<domain>`, with runtime logs and backups |
| Stacks | One project with multiple services; each service gets its own subdomain, port, process, health check and logs, with shared repo/env and private `127.0.0.1` service URLs |
| Addons | 19 engines — relational databases, caches, queues, search, metrics, logs, notifications, Git and backend services — installed and supervised on a phone; standalone or created with a project; injected env, relay for other phones, PC bridge, SQL console, chunked backups, standbys, manual promotion and opt-in automatic failover |
| UI | Light theme by default, with a theme studio: Light / Dark / Match-system, 8 accent presets, custom primary and secondary colors, persisted per browser |
| Scheduling | 1–5 replicas per project, label selectors, pinned phones, health-based placement, automatic failover, manual rebalance |
| Routing | `<slug>.<domain>` round-robins across running replicas through the agent tunnels; drained/offline replicas are skipped |
| Env | Per-project variables, encrypted at rest with AES-256-GCM, injected into every replica |
| Alerts | Offline, temperature, battery, disk, crash-loop, failed-deploy and database alerts; thresholds, ack, history, Telegram and webhook notifications |
| Audit log | Cursor-paginated events (25–200 per page), action filter, search across action/target/detail, live refresh |
| Edge | `phleet setup-cloudflare` creates the tunnel, wildcard DNS, ingress and the Windows service; config reloads every 15 s |
| Security | Signed session cookies, constant-time token checks, agent bearer auth, host-header guard, path sandbox with symlink resolution, panel cookies stripped from app traffic |

## Requirements

- Windows 10/11 for the control plane (the binaries also build for Linux/macOS)
- Go 1.22+ and Node 20+ to build
- A domain on Cloudflare (free plan is fine) for public access
- `cloudflared` installed if you want the service auto-installed
- Phones with [Termux](https://f-droid.org/packages/com.termux/) (F-Droid build, not the Play Store one)
- Optional: `postgresql`, `redis`, `sqlite` and `rclone` Termux packages (installed on demand, or up front with the
  bootstrap's `--with` flag)

## Quickstart

```powershell
# 1. build everything
.\scripts\build.ps1

# 2. run the control plane (prints the admin and agent tokens)
.\dist\phleetd.exe -addr 127.0.0.1:8080 -print-tokens
```

Open <http://127.0.0.1:8080>, log in with the admin token.

```powershell
# 3. optional: public edge, one time for the whole fleet
.\dist\phleet.exe setup-cloudflare -domain example.com
```

This creates:

- tunnel `phleet`
- `CNAME fleet.example.com → <tunnel>.cfargotunnel.com` (proxied)
- `CNAME *.example.com → <tunnel>.cfargotunnel.com` (proxied)
- one catch-all ingress rule to `http://127.0.0.1:8080`
- the `cloudflared` Windows service

Every future project is just a row in the database — no further Cloudflare calls. `phleet doctor` verifies tunnel, DNS, nameservers and the local API.

```powershell
# 4. install the Windows service so it starts at boot
.\deploy\windows\install-service.ps1
```

### Add a phone

From the panel, open **Fleet → Add phone** (or the **+** next to “Phones” in the sidebar):

- **Install over SSH** — enter the name, an SSH target (`phone-termux`, a `~/.ssh/config` alias, or `user@host`), the port if it is not in the config, and an identity file if needed. The control plane runs the bootstrap over SSH and streams the output. The phone only needs Termux with `sshd` and key-based login.
- **Manual** — copy the generated command into Termux. This works without SSH.

The **Control plane URL** field must be an address the phone can reach: your LAN URL (`http://192.168.1.2:8080`, with the firewall rule) or the public Cloudflare URL. It is pre-filled from the UI address, but you can edit it.

If `phleetd` runs as a Windows service (SYSTEM), it cannot see your user’s `~/.ssh/config` or keys. In that case use `user@host` plus an explicit identity file that SYSTEM can read, or run the bootstrap manually.

The bootstrap script can also be run directly in Termux:

```bash
curl -fsSL https://fleet.example.com/download/bootstrap.sh \
  | bash -s -- --server https://fleet.example.com --token <agent-token>
```

On the LAN without a tunnel, use `--server http://<pc-lan-ip>:8080` instead. The script installs the agent, writes `~/.phleet/agent.env`, registers a `termux-services` runit unit (falls back to `nohup`), acquires a wake lock and installs a Termux:Boot hook.

Add `--with postgres,redis,sqlite,rclone` to preinstall data engines; otherwise phleet installs an engine the first time
you create a database on that phone (which needs network access and a few minutes).

Then, on the phone:

1. Set Termux battery usage to **Unrestricted**.
2. Optional but strongly recommended — stop Android's phantom process killer once over ADB:
   `adb shell device_config put activity_manager max_phantom_processes 2147483647`
3. Install Termux:API for reliable battery readings.

#### Rooted Android without Termux

Termux is not required to run the agent, only to run *work*. There are two paths:

**Debian chroot (full node).** If the phone already has a Linux container (Podroid, Linux Deploy, Andronix), the agent
runs inside it as an ordinary user. It then behaves exactly like a Termux phone: deploys, data addons, package installs
with `apt`. The chroot must expose `/proc` and `/sys` (most setups bind-mount them) and its user must be in group
`3003` (`inet`) — Android blocks sockets for UIDs outside it, which otherwise looks like broken DNS.

```powershell
scp scripts/bootstrap-debian-chroot.sh root@<phone>:/data/local/tmp/
ssh root@<phone> "sh /data/local/tmp/bootstrap-debian-chroot.sh \
  --server https://fleet.example.com --token <agent-token> --name pixel-4a"
```

The script creates the unprivileged `phleet` user, adds it to `inet`, installs the agent with the control plane's
`curl`, writes `/home/phleet/.phleet/run-agent.sh`, and registers a Magisk `service.d` hook that starts it inside the
chroot after boot. `--with postgres,redis,sqlite,rclone,node` preinstalls engines with apt; otherwise the panel
installs one on first create. PostgreSQL refuses to run as root, which is why the agent never runs as root here.

**Bare root (monitoring only).** Without a userland the same binary still runs from `/data/local/tmp` and reports
metrics, battery, thermal, shell and files. Deploys and addons are disabled by the panel, because there is no git or
package manager for the agent to call.

```powershell
adb push dist/bin/phleet-agent-linux-arm64 /data/local/tmp/phleet/bin/phleet-agent
adb push scripts/bootstrap-android-root.sh /data/local/tmp/phleet/
adb shell "su -c 'sh /data/local/tmp/phleet/bootstrap-android-root.sh --server https://fleet.example.com --token <agent-token>'"
```

The script writes `agent.env` and a `run-agent.sh` that sets `HOME`/`TMPDIR` under `/data/local/tmp`, and registers a
Magisk `service.d` hook so the agent starts at boot. Add `--root /data` if you want the file browser to see the whole
device. The panel shows the phone as **monitoring only** and will not schedule projects or addons on it.

### Deploy a project

Open **Projects → New project**. Choose the source:

- **Git repository** — repo URL + branch; the phone clones it.
- **Upload folder** — pick a folder from your machine. It is zipped in the browser (top-level folder stripped), uploaded to the control plane, and every agent downloads the bundle at deploy time. No git needed on the phone.

| Field | Example | Notes |
|---|---|---|
| Start command | `node server.js` | must listen on `$PORT` |
| Build command | `npm ci --include=dev && npm run build` | optional, runs in the release directory |
| Health path | `/health` | TCP-only check when empty, `200–499` counts as healthy |
| Environment | `NODE_ENV=production` | encrypted on the control plane |

Builds inherit the runtime environment, including `NODE_ENV=production`. Pass `--include=dev` (or set `NPM_CONFIG_PRODUCTION=false`) when a build needs devDependencies such as `tsc` or Vite.

Deploying runs entirely on the phone: download/clone → build → stop old release → start new → health check →
symlink `current` → keep five releases. If the new release fails its check, the previous one is restarted and the
deployment is marked `rolled_back`. A phone that reboots re-supervises its apps from `~/.phleet/apps.json`.

Uploaded projects can replace their source later under **Project → Settings → Source** (upload a new folder, then
Deploy). Source archives are capped at 64 MB; empty directories are not preserved.

A single project is **one supervised process listening on one port behind one hostname**. If a repository contains a UI,
an API and a worker, use a **multi-service stack** so the UI still treats them as one project:

- One stack owns the shared repo, branch, build source and encrypted environment.
- Each service has its own **subdomain**, port, process, health check, releases and logs.
- Pin the stack to one phone so services can reach each other over `127.0.0.1`.
- `PHLEET_SERVICE_<SLUG>_URL` is injected into every service on that phone, e.g.
  `PHLEET_SERVICE_API_URL=http://127.0.0.1:4102`.
- **Deploy / Restart / Stop** on the stack applies to every service; open a service card for its logs and settings.

The Cloudflare wildcard covers every service subdomain automatically, so adding a service needs no DNS or tunnel
change. phleet still does not read `docker-compose.yml`, provide shared volumes or private Docker networks; for
containers on a phone use Podroid + Compose. For a database inside a stack, create it from the project wizard — the
stack pins every service to one phone, which is exactly what a loopback database needs.

**Stack lifecycle includes its databases.** `Stop` stops the services and then the addons that belong to the stack;
`Start` brings those addons up before the services. Deleting a stack shows a second warning listing the databases it
owns, then deletes them and their data (a database also attached to another project is kept). Back up anything you
need before confirming.

The app receives `PORT`, `HOST=0.0.0.0`, `NODE_ENV=production`, `PHLEET_APP` and your variables. Its URL is
`<slug>.<domain>` once Cloudflare is configured, or `<slug>.localhost` for local use (Chrome resolves any
`*.localhost` to loopback).

### Multi-service stacks

Create one from **New project → Multi-service stack**. Give the stack a name and source, pin it to a phone, then add
services:

| Field | Example |
|---|---|
| Name | `Web` |
| Subdomain | `web` |
| Build command | `pnpm install --frozen-lockfile && pnpm --filter web build` |
| Start command | `pnpm --filter web start` |
| Health path | `/health` |
| Service env | `NEXT_PUBLIC_API_URL=https://api.example.com` |

Leave a subdomain blank to use the stack slug for the first service and `<stack>-<service>` for the rest. Services
share the stack environment; per-service values override it at deploy time. The stack view shows each service's
subdomain, phone, port, PID, restart count, latest deploy and last error, with a link to its full project page.

Builds run on the phone, so keep them phone-sized. Next.js 16 defaults to Turbopack, which has no Android native
bindings; build with webpack instead (`next build --webpack`, or a `build:webpack` script). `pnpm install
--frozen-lockfile` works unchanged in a workspace. See [docs/deploying-stacks.md](docs/deploying-stacks.md) for a
worked monorepo example, including a web service whose `/health` proxies to the API service.

Stacks are still plain processes, not containers: no isolation, image builds or shared volumes. Use Podroid + Compose
for those.

### Services

A **service** is a ready-built self-hosted app installed from a catalogue — the app itself, not a dependency and not
your source code. Services skip the project deploy pipeline: phleet downloads the release, initialises a data directory,
supervises the process, gives it a subdomain and exposes logs and backups.

Open **Services → Catalogue** and pick an app, then choose the phone that will run it. The first install downloads the
release; later installs are instant. Services are pinned to one phone (their data lives there) and are exposed at
`<slug>.<domain>`, exactly like projects.

The first wave is deliberately single-binary and phone-friendly:

| Service | Category | Notes |
|---|---|---|
| **Filebrowser** | files | web file manager; files live under the service data directory |
| **Memos** | notes | Markdown notes and tags |
| **Gotify** | notifications | push notifications with a web UI |
| **MicroBin** | utilities | pastebin and file sharing |
| **Navidrome** | media | music streaming; put audio in the service's `music/` folder |
| **Syncthing** | files | continuous file synchronisation |
| **Vikunja** | productivity | lists, kanban and teams; full release tree with frontend |
| **Miniflux** | reading | RSS reader; needs a PostgreSQL addon — paste its `DATABASE_URL` when installing |

Each service has an Overview page (URL, phone, port, data directory, version, placement), runtime **Logs**, and
**Backups** with create/download/restore. Open the URL after install to finish the app's own first-run setup.

Services and addons are separate sections but share the same supervisor, port allocator, bridge, tunnel router,
backups and alerting. A service is the app; an addon is what an app depends on.

### Addons

An addon is any supporting service beside projects: a database, cache, object storage, analytics or monitoring
process. phleet installs the engine, initialises a data directory outside the release tree, supervises the process,
backs it up and (for PostgreSQL and Redis) replicates it. Nothing is containerised and nothing listens on `0.0.0.0`.

| Engine | How it runs | Notes |
|---|---|---|
| **PostgreSQL** | `postgres -D ~/phleet/data/<slug>` | ~160 MB RAM; WAL recovery; streaming standby |
| **MariaDB** | `mariadbd --datadir=~/phleet/data/<slug>` | MySQL protocol for apps that expect `mysql://`; dump/restore; no standby |
| **Redis** | `redis-server ~/phleet/data/<slug>/redis.conf` | ~64 MB; AOF persistence; async standby |
| **InfluxDB** | `influxd` with data dirs under `~/phleet/data/<slug>` | time series for metrics; HTTP API; portable backup; no standby |
| **SQLite** | no process, just a file | cheapest and most durable for a single app; no standby |
| **DuckDB** | no process, just a file | embedded analytics; SQL console over Parquet/CSV/JSON; no standby |
| **S3 storage** | `rclone serve s3 ~/phleet/data/<slug>` | object storage over HTTP, published on its own hostname |
| **Memcached** | `memcached -l 127.0.0.1` | ~16 MB; in-memory cache, no persistence or backup |
| **Mosquitto** | `mosquitto -c …` | ~16 MB; MQTT broker with a persistent message store |
| **NATS** | `nats-server -js` | ~32 MB; messaging and JetStream streams |
| **VictoriaMetrics** | `victoria-metrics -storageDataPath …` | ~128 MB; Prometheus-compatible metrics store |
| **VictoriaLogs** | `victoria-logs -storageDataPath …` | ~64 MB; log store with a LogsQL query API |
| **ntfy** | `ntfy serve …` | ~32 MB; self-hosted push notifications over HTTP |
| **PocketBase** | `pocketbase serve …` | ~32 MB; SQLite-backed backend, auth, realtime and admin UI |
| **etcd** | `etcd --data-dir …` | ~64 MB; coordination, locks and configuration |
| **Meilisearch** | `meilisearch --http-addr …` | ~128 MB; full-text search; large (~350 MB) binary |
| **Prometheus** | `prometheus --config.file …` | ~256 MB; metrics scraper and time-series database |
| **Gitea** | `gitea web …` | ~128 MB; self-hosted Git with issues, PRs and a web UI |
| **Beanstalkd** | `beanstalkd -l 127.0.0.1` | ~16 MB; work queue; Debian package only |

> MinIO's community binaries were retired in 2025 (`dl.min.io` returns 410). phleet uses **rclone**, which is packaged
> in Termux, actively maintained, and serves a normal directory as an S3 bucket.

Addons are not limited to data services. Logging, monitoring, messaging, notifications and backend services use the same
lifecycle: install, initialise, supervise, expose on loopback, inject environment variables and optionally back up.
Engines with a **static installer** (NATS, VictoriaMetrics, VictoriaLogs, ntfy, PocketBase, DuckDB, rclone) do not need
a package manager or root; this matters on a Debian chroot where `/data` is mounted `nosuid` and `sudo` cannot work.
Packaged engines (Memcached, Mosquitto, PostgreSQL, MariaDB, Redis, InfluxDB) can be installed once as root with
`apt-get install`, or preinstalled with the bootstrap's `--with` flag.

Some engines are platform-specific: **Beanstalkd** is offered only on Debian/apt phones (the Termux package index
has no Beanstalkd). **etcd** uses a fixed peer port (`2380`), so run one etcd addon per phone.

Create one in two places:

- **Addons → New addon** — standalone. It can later be attached to one or more projects.
- **New project → Create an addon** — inline. It is created with the project, owned by it and attached automatically.

An addon is **pinned to one phone** and binds to `127.0.0.1`. Apps on the same phone receive:

```bash
DATABASE_URL=postgres://app:<generated>@127.0.0.1:4100/app
PGHOST=127.0.0.1 PGPORT=4100 PGUSER=app PGPASSWORD=… PGDATABASE=app
# Redis instead:
REDIS_URL=redis://:<generated>@127.0.0.1:4101/0
# S3 instead:
S3_ENDPOINT=http://127.0.0.1:4102 S3_BUCKET=media S3_ACCESS_KEY_ID=… S3_SECRET_ACCESS_KEY=…
```

Apps on **another phone** get a loopback facade instead: the agent on that phone opens a relay over its existing
outbound tunnel, the control plane forwards it to the database phone, and the injected URL still points at
`127.0.0.1`. No inbound port, no Cloudflare rule, no redeploy when the primary moves. The values are encrypted at
rest, injected on every deploy, and also exposed as `PHLEET_ADDON_<NAME>_*`.

Three ways to reach a data service:

| From | How |
|---|---|
| An app on the same phone | direct loopback (`DATABASE_URL`) |
| An app on another phone | automatic relay facade (`DATABASE_URL`, `PHLEET_ADDON_*_RELAYED=true`) |
| Your PC | **bridge**: `psql -h 127.0.0.1 -p 15400` (port shown on the detail page); opt-in `-bridge-lan` exposes it on the LAN |
| The internet | the panel console; S3 addons get `s3-<name>.<domain>` through the normal wildcard tunnel |

S3 request bodies are capped at 96 MB (Cloudflare's per-request limit). Start `phleetd` with
`-storage-max-mb 4096` when the control plane is reached directly over the LAN.

The detail page gives you:

- **Console** — one-shot SQL or Redis commands, piped to `psql`/`redis-cli`/`sqlite3` on the phone.
- **Backups** — dump on the phone, upload in 32 MB chunks (so a large dump survives Cloudflare's 100 MB request cap),
  keep the last 10, download or restore. An automatic daily backup runs when the phone is charging or above 40 %.
- **Replication** — add a standby on another phone, watch replay lag, promote manually after the old primary is
  fenced. PostgreSQL uses streaming replication; Redis uses `REPLICAOF`. SQLite and S3 have no standby. **Automatic
  failover** is opt-in per addon: the control plane is the witness, the primary renews a lease, and the agent
  self-fences when the panel is unreachable. See
  [docs/runbooks/database-failover.md](docs/runbooks/database-failover.md) for the exact procedure and the
  panel-down trade-off. Promotion advances an **epoch**; a demoted primary that reconnects is stopped and cannot
  serve writes. Run [the two-phone rehearsal](docs/runbooks/two-phone-rehearsal.md) once before trusting it with
  data.
- **Logs** — package install, `initdb`, health checks, relay, backup/promote/restore output.
- **Start / stop / restart** — the same supervisor that runs projects.

**Replicas are deliberately conservative.** A database has one primary and one copy of its data. phleet will not
create an empty database to satisfy a replica count, will not move an addon automatically, and will not fail over
without an explicit promote. Consequences:

- A **SQLite** database is a file, so its project must run **one replica on that phone**. PostgreSQL, Redis and S3
  work with multi-replica projects because the other phones reach them through the relay.
- A standby is not a backup. A second copy on the same Wi-Fi is not a backup. Keep the off-device dumps.
- Promotion fences the old primary first. If it is offline it is marked demoted and stopped when it reconnects; there
  is no quorum, so automatic promotion would risk split-brain.

**Raw TCP and Cloudflare.** The tunnel in front of the panel is HTTP ingress. Cloudflare Tunnel can carry arbitrary
TCP only when the client also runs `cloudflared access tcp` (an Access/SSO application), or with Spectrum (an
Enterprise add-on). It cannot expose `db.example.com:5432` to a plain `psql`. phleet therefore uses its own tunnel for
database traffic: loopback, the relay, and the control-plane bridge. S3 is HTTP, so it is the one data service that is
published through the normal wildcard tunnel.

> **Phones are not database servers.** Keep `shared_buffers` small, expect thermal throttling, and remember that
> sustained writes wear eMMC. Take backups before you need them, and do not put a database on a phone you cannot
> physically reach.

### Replicas, labels and failover

Run more than one copy of a project and let the scheduler spread them:

- **Replicas** — 1–5 per project. Each replica gets its own port on its own phone.
- **Label selector** — e.g. `tier=web, region=home`. Set labels on each phone under **Server → Settings**.
  The project only lands on phones matching every key.
- **Pin** — optionally force a project onto one phone instead of using the scheduler.
- **Drain** — `Server → Settings → Drain` removes a phone from routing and new placements; the reconciler moves its
  apps to other matching phones, then stops them on the drained device.

Routing round-robins across healthy replicas. If a phone disappears, traffic immediately shifts to the remaining
replicas; the reconciler adds a replacement on another eligible phone and only removes the stale placement once the
replacement reports `running`. Use **Rebalance** on a project to reconcile immediately. With `replicas=1` a phone loss
means downtime until the replacement is healthy — use two replicas for zero-downtime failover.

A multi-replica project can use PostgreSQL, Redis or S3 because replicas on other phones reach it through a loopback
relay over their agent tunnel. A SQLite file cannot be relayed, so those projects stay at one replica on the file's
phone.

### Alerts

The control plane evaluates every 30 seconds:

| Alert | Default |
|---|---|
| Phone offline | 120 s without a heartbeat |
| High temperature | ≥ 60 °C (critical at 70 °C) |
| Low battery | below 20 % and not charging |
| Disk almost full | ≥ 90 % used (critical at 95 %) |
| App crash-looping | placement status `crashed` |
| Database unhealthy | addon status `failed` (critical) or `unreachable` (warning) |
| Deploy failed | latest deployment `failed` |

Alerts open once, update in place, can be acknowledged, and resolve automatically. Configure thresholds and
Telegram/webhook notifications under **Alerts → Rules & notifications**; the test button sends a message immediately.
No notification credentials? Alerts still appear in the panel and the sidebar badge.

## Using it

- **Fleet** — every phone with live CPU/temp/memory/battery; click through for charts.
- **Server → Terminal** — a real PTY in the browser, with quick commands (`battery`, `disk`, `top`, wake-lock).
- **Server → Files** — navigate from `$HOME`, edit config files inline, upload single/multiple files or an entire
  folder (zipped in the browser, extracted on the phone), download, manage directories.
- **Server → Processes** — top processes by RSS, filterable.
- **Server → Overview** — system facts, detected toolchain, labels, run-once command box.
- **Server → Settings** — rename, edit labels, drain/resume, remove the device.
- **Projects** - deploy, watch live build logs, tail app logs per replica, restart/stop/start, roll back, edit env,
  change replicas/selectors, rebalance, attach a database.
- **Stacks** - one project with several services, each with its own subdomain, port and health check.
- **Services** - install Filebrowser, Memos, Gotify, MicroBin, Navidrome, Syncthing, Vikunja or Miniflux from the
  catalogue, then open its URL, logs and backups.
- **Addons** - create PostgreSQL/Redis/SQLite/S3, open the console, use the PC bridge, add a standby and promote it,
  take/restore backups, read provisioning logs.
- **Alerts** — active alerts, history, thresholds and notification channels.
- **Theme** — light by default. The sidebar palette button and **Settings → Appearance** open the theme studio:
  Light / Dark / Match system, eight accent presets, and custom primary/secondary colors. The choice is remembered
  per browser.

## Security notes

- The panel binds to `127.0.0.1` by default; only cloudflared reaches it.
- Put **Cloudflare Access** in front of `fleet.example.com` (Zero Trust → Access → Applications), with a path bypass for `/agent` and `/agent/data` so phones can connect.
- One agent token covers the fleet today. Per-device tokens and mTLS are on the roadmap.
- The file API is confined to `$HOME` (plus `--root` directories you pass explicitly) and resolves symlinks before every check.
- Panel tokens never leave the host through the API, and the session cookie, `Authorization` and `X-Phleet-Token` headers are stripped before requests are proxied to project apps.
- Project environment variables are encrypted at rest with AES-256-GCM using a key derived from the control plane secret.
- The PC database bridge binds `127.0.0.1` by default. `-bridge-lan` exposes it on the LAN; the engine still
  requires its generated password, but treat it as a deliberate opt-in.
- Addon relays are per addon and per phone. An agent can only open a relay for an addon the control plane explicitly
  assigned to it, and the relay terminates at the addon's primary port.
- Addon credentials use the same encryption. Backups are dumps of real data and are **not** encrypted at rest on the
  control plane; remove them from the addon page when you no longer need them.

Known proxy limits: HTTP requests to project apps are capped at 32 MB, phleet uploads at 64 MB and downloads at 96 MB,
and only ports phleet allocated to a supervised app are reachable. S3 bodies stream through a 96 MB cap (stay under
Cloudflare's 100 MB request limit; S3 clients use multipart for larger objects). Backup uploads use 32 MB chunks.
WebSocket upgrades work through the tunnel, but long-lived connections count against the phone's wake-lock and battery
budget. Folder and project archives are capped at 64 MB and extracted server-side; source bundles are stored on the
control plane as zip files (not encrypted at rest, unlike project environment variables).

Running phones as servers has real limits: no cgroups, no containers without root, eMMC wear, thermal throttling and an OS that actively kills background work. phleet is designed for a personal fleet, not untrusted multi-tenant workloads.

## Troubleshooting phones

| Symptom | Cause and fix |
|---|---|
| SSH to `phone:8022` is refused | Termux's sshd is not running. Open Termux and run `sshd` (or `sv up sshd`). The agent itself does not need SSH. |
| Add phone over SSH fails with `Permission denied (publickey)` | The control plane has no key for that target. Run phleetd as your user, or pass an explicit identity file; a SYSTEM service does not see your `~/.ssh/config` or keys. |
| `SIGSYS: bad system call` in `~/.phleet/agent.log` | Agent older than 0.3.1 called `exec.LookPath`, which uses `faccessat2`; Android 10's seccomp policy kills it. Update the agent (re-run the bootstrap) — 0.3.1 resolves binaries with `stat` instead. |
| Agent dies when opening the terminal or deploying | Agent older than 0.3.2 used `pidfd_open` (Go 1.23+) when starting a process, which Android 10 blocks. 0.3.2's arm64 agent is built with Go 1.22. Update with the bootstrap; do not rebuild the arm64 agent with the newer toolchain. |
| `certificate signed by unknown authority` | Android has no `/etc/ssl/certs`. 0.3.1 auto-detects `$PREFIX/etc/tls/cert.pem`; the bootstrap also writes `SSL_CERT_FILE` into `~/.phleet/agent.env`. |
| `no addresses for fleet.example.com` | Termux has no `/etc/resolv.conf`, so Go's resolver fails. 0.3.1 resolves the control-plane hostname with DNS-over-HTTPS (1.1.1.1, then 8.8.8.8). If a network blocks those, point the agent at a LAN/IP endpoint instead. |
| CPU, temperature, network and uptime show `—` | Non-root Android 10 hides `/proc/stat`, `/proc/net/dev`, `/sys/class/thermal` and `/sys/class/power_supply` from apps. Memory and disk still work. Install **Termux:API** (`pkg install termux-api` and the app) for battery, or root the phone for the rest. |
| Phone disappears after a while | Disable battery optimisation for Termux, keep the wake lock, and install `termux-services` so the agent is supervised by runit instead of `nohup`. |
| Phone shows offline for a minute every ~90s | Agent older than 0.4.0 called `termux-battery-status` on the heartbeat path. With the Termux:API **app** missing, that helper blocks inside a binder call, heartbeats stop and the hub closes the idle connection. 0.4.0 samples battery and metrics off the heartbeat path; update the agent. |
| `sv restart phleet-agent` says `runsv not running` | Termux was closed and `runsvdir` died while the agent process survived. Start the supervisor again with `setsid runsvdir -P $PREFIX/var/service`, then `sv restart phleet-agent`. Reopening Termux also starts it. |
| Project shows **stopped** after the phone reconnects, but its URL still answers | Agent older than 0.10.1 could not re-adopt processes that outlived it. Android denies `/proc/net/tcp` to apps, so the old port check found no owner and the app stayed marked stopped while the orphan kept serving. 0.10.1 writes a pidfile and adopts by working directory. Update the agent (re-run the bootstrap or replace `~/.phleet/bin/phleet-agent`). |
| Addon stuck at `provisioning` | The phone is installing a package. Open **Addons → Logs**. On Termux the agent installs it with `pkg` as your user; on a Debian chroot the agent is unprivileged (Postgres must not run as root) and `/data` is `nosuid`, so install the engine once as root (`chroot /data/debian/rootfs apt-get install -y postgresql`). The panel re-probes capabilities every 5 minutes and then creates the addon normally. |
| Postgres health check fails after a reboot | The supervisor restarts it from `~/.phleet/apps.json`; if it crash-loops, read **Addons → Logs** and `~/apps/db-<name>/logs/app.log`. Never delete the data directory to "fix" it — take a backup first. |
| `could not create shared memory segment: Function not implemented` during `initdb` | The phone's kernel has no SysV IPC (`CONFIG_SYSVIPC` disabled), so PostgreSQL cannot start there. Use MariaDB or SQLite, or a kernel/chroot that exposes SysV shared memory. Miniflux needs PostgreSQL and will not run on such a phone. |
| Database attach refused | A database is pinned to one phone and a project that uses it must run one replica on that same phone. Pin the project, or create the database from the project wizard so it lands there automatically. |
| Backup upload fails | Backups are uploaded in 32 MB chunks, so a large dump is not limited by Cloudflare's 100 MB request cap. A failed chunk marks the backup `failed`; check free space under `<data>\backups` and retry. |
| Standby stuck at `provisioning` | It clones from the primary through a relay. Open **Addons → Logs**; `pg_basebackup` errors usually mean the primary is not running, the replication role is missing, or the relay port is blocked. |
| Promote refused | The standby must be `running` and have reported lag. Promotion also stops the old primary first; if that call fails the promote is refused instead of risking split-brain. |
| `rclone: not found` | The S3 engine needs `pkg install rclone`, or the bootstrap's `--with rclone`. |

## Development

```powershell
# control plane with verbose logs
.\dist\phleetd.exe -addr 127.0.0.1:8080 -debug

# UI dev server (proxies /api and websockets to :8080)
cd web; npm run dev
```

> `scripts/build.ps1` keeps symbols in the Windows binaries. Some Application Control /
> Smart App Control policies reject stripped Go executables with "did not meet the enterprise
> signing level requirements". The Linux agent is still stripped for download size.
>
> The arm64 agent is deliberately built with **Go 1.22** (`go122.mod` / `go122.sum`). Go 1.23+
> calls `pidfd_open` when starting a process, and Android 10's seccomp policy kills that with
> `SIGSYS`, which breaks terminals and deploys. Keep that split when editing the build script.

```powershell
# tests (some Application Control policies block binaries under %TEMP%)
$env:GOTMPDIR = "$PWD\dist\gotmp"; go test ./...

# real PostgreSQL/Redis replication tests (Linux/WSL, no root)
# build the Linux test binary on the host, then run it where the servers can run:
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go test -c -o dist\lintest\engines-live.test ./internal/engines
wsl -d Ubuntu -- bash scripts/run-live-tests.sh

# two-agent PostgreSQL rehearsal: two agents, relay-based standby, promotion,
# epoch fencing and the control-plane bridge (no root, portable PostgreSQL)
wsl -d Ubuntu -- bash scripts/run-two-agent-pg.sh
```

The live script pulls a portable PostgreSQL (zonky embedded build) and the Ubuntu Redis packages into
`~/.phleet-test`; it exercises `initdb`, `pg_basebackup`, standby start, lag, promotion and the Redis
equivalent, including the actual engine command templates.

# full deploy smoke test: real control plane + agent + git repo + tunnel
# (Linux or WSL; build.ps1 publishes the linux binaries)
wsl -d Ubuntu -- bash -lc "./scripts/e2e-linux.sh /mnt/c/Users/<you>/Projects/phleet"
```

Layout:

```
cmd/phleetd          control plane: API, hub, UI, edge reload
cmd/phleet-agent     static linux/arm64 agent for Termux
cmd/phleet           CLI: setup-cloudflare, doctor, servers, token
internal/agent       supervisor, deploys, metrics, PTY, filesystem sandbox
internal/agentproto  wire protocol
internal/hub         live connections, request/response, PTY routing, tunnels
internal/httpapi     REST + WebSocket API, auth, host guard, project router
internal/store       SQLite schema and queries
internal/tunnel      yamux-over-WebSocket + HTTP proxying
internal/engines     shared addon + service catalogue (commands, env, backups)
internal/secrets     AES-GCM for project env and addon credentials
internal/cloudflare  API client + persisted tunnel config
internal/webui       embedded React build
web/                 React + Vite + Tailwind UI
scripts/             build, Termux bootstrap, Linux e2e, portable DB test harness
deploy/windows/      scheduled task installer
docs/runbooks/       database failover procedure
```

See [docs/architecture.md](docs/architecture.md) for the protocol, failure model and deploy design.

## Roadmap

1. **Addons** — S3 bucket mirroring between two phones, automatic promotion with a quorum/witness, per-addon quotas.
2. **Scale** — custom domains, build caching, scheduled jobs.
3. **Security** — per-device tokens, API tokens and RBAC, audit export.
4. **Ops** — restore drills, batch commands, mobile polish.
