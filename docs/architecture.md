# phleet architecture

## 1. Goals

phleet is a small PaaS for hardware people already own. An old Android phone is a
capable always-on computer — CPU, RAM, flash and a battery that acts as a UPS — but it is
not a server you can SSH into from the internet, and it aggressively kills background
work. phleet makes a handful of them behave like a private cloud:

- Operate a handful of Android phones running Termux from one UI.
- Treat the phones as a pool for small web projects: deploy, run, observe.
- Run the supporting data services (databases, caches, queues, object storage) on the same
  phones, next to the apps that use them.
- Never open an inbound port on a phone or on the Windows machine.
- Keep working — as far as possible — when the control plane is asleep.

The intended user is an individual or small team with a drawer of retired phones, not a
cloud provider. Non-goals: multi-tenant isolation, container orchestration, running
untrusted code.

## 2. Topology

```
                    ┌───────────────────────────────┐
  Internet ────────▶│ Cloudflare (DNS, TLS, Access) │
                    └───────────────┬───────────────┘
                                    │ outbound-only tunnel
                                    ▼
                    ┌───────────────────────────────┐
                    │ cloudflared (Windows service) │
                    └───────────────┬───────────────┘
                                    │ http://127.0.0.1:8080
                                    ▼
                    ┌───────────────────────────────┐
                    │ phleetd                       │
                    │  API · UI · schema · router   │
                    │  hub (live agent sessions)    │
                    │  SQLite                       │
                    └───────────────▲───────────────┘
                                    │ WSS /agent (agent dials out)
              ┌─────────────────────┴─────────────────────┐
              ▼                                           ▼
      ┌───────────────┐                           ┌───────────────┐
      │ fleet-agent   │  …                        │ fleet-agent   │
      │ Termux/arm64  │                           │ Termux/arm64  │
      └───────┬───────┘                           └───────┬───────┘
          apps :41xx                                  apps :41xx
```

One hostname serves the panel (`fleet.<domain>`) and, later, every project (`<slug>.<domain>`). The control plane routes by the HTTP `Host` header, so the tunnel configuration never changes.

## 3. Why not just SSH?

SSH is excellent for bootstrap but a poor data plane here:

- phones are behind CGNAT and change IPs, so the control plane cannot dial in;
- shell output parsing is brittle, streaming needs PTYs anyway, and per-request connections are expensive;
- an agent can keep apps running and buffer state while the control plane is unavailable.

So SSH is used for the initial device setup (and optionally for break-glass work), while a persistent agent connection carries everything else. The browser terminal does not depend on `sshd`: the agent opens a PTY locally and streams it over the tunnel.

## 4. Wire protocol

Transport: one WebSocket per agent at `wss://fleet.<domain>/agent`, authenticated with `Authorization: Bearer <agent-token>`. Every frame is a JSON envelope:

```json
{ "type": "exec.request", "id": "uuid", "replyTo": "", "error": "", "data": { } }
```

Request/response is correlated by `id` → `replyTo`. Binary payloads (PTY chunks, file content) are base64 inside JSON; at this scale the simplicity is worth more than the overhead.

| Type | Direction | Purpose |
|---|---|---|
| `hello` | agent → CP | identity, hostname, arch, version, capability map |
| `hello.ack` | CP → agent | confirms registration and, on first enrollment, delivers the per-device token the agent persists to `agent.token` |
| `heartbeat` | agent → CP | metrics + supervised app states every 5 s |
| `exec` | CP → agent | run a shell command with timeout |
| `pty.open/data/resize/close` | both | terminal lifecycle, raw bytes |
| `pty.exit` | agent → CP | exit code, UI shows "session ended" |
| `fs.list/read/write/mkdir/rename/delete/stat` | CP → agent | sandboxed file operations |
| `fs.write.chunk` | CP → agent | uploads split into sub-2 MB chunks (WebSocket read limit) |
| `fs.extract` | CP → agent | safe zip extraction into a directory |
| `proc.list` | CP → agent | process snapshot for the UI |
| `deploy.request` | CP → agent | self-contained deploy plan |
| `deploy.event` | agent → CP | status transitions and log lines |
| `app.logs` | CP → agent | tail of the supervised app's log |
| `app.control` | CP → agent | start, stop, restart, rollback, delete |
| `addon.provision` | CP → agent | self-contained engine install + init + start plan |
| `addon.control` | CP → agent | start, stop, restart, delete (optionally purge the data) |
| `addon.backup` / `addon.restore` | CP → agent | engine-native dump/restore; the file moves over the authenticated HTTP API |
| `addon.event` | agent → CP | provisioning, backup, restore logs and terminal status |
| `addon.relay` | CP → agent | start/stop a loopback facade that forwards to an addon on another phone |
| `addon.promote` | CP → agent | promote a standby after the control plane fenced the old primary |

The hub keeps a pending map per connection, a 90 s read deadline (heartbeats keep it alive), and per-session PTY channels. A slow UI consumer drops chunks instead of blocking the agent read loop.

Reconnect uses exponential backoff (1 s → 30 s) with jitter. A successful connection lasting more than 60 s resets the backoff.

### 4.1 Data tunnel

HTTP traffic to projects uses a second WebSocket per agent at `/agent/data`, authenticated with the same bearer
token plus an `X-Phleet-Server` header. `internal/tunnel.WSConn` adapts the WebSocket into a `net.Conn`, and
[`yamux`](https://github.com/hashicorp/yamux) runs a stream multiplexer on top. The control plane opens one stream
per HTTP request; the agent accepts it, dials `127.0.0.1:<port>` and speaks HTTP/1.1 to the app.

Stream framing:

```
[4-byte length][JSON RequestHeader{port, method, path, host, headers, contentLength}]
[request body, exactly contentLength bytes]
[4-byte length][JSON ResponseHeader{status, headers, contentLength}]
[response body until stream close]
```

Request bodies are buffered (32 MB cap) so the agent never needs half-close support. Hop-by-hop headers are
filtered, `phleet_session`, panel bearer tokens and `X-Phleet-Token` are stripped, and `X-Forwarded-*` headers are
added. WebSocket upgrades are bridged as raw bytes after the `101` handshake (long-lived streams have no wall-clock
deadline); other protocol upgrades and HTTP/2 to the origin are not supported. Only ports allocated to a supervised
app are reachable.

## 5. Data model (SQLite, WAL)

| Table | Contents |
|---|---|
| `servers` | identity, capabilities (JSON), labels, status, last_seen |
| `metrics` | one row per heartbeat; indexed on `(server_id, ts)` |
| `events` | audit/activity feed |
| `projects` | slug, repo, commands, health path, hostname, encrypted env, selector, replicas, pinned phone, source type/size |
| `placements` | project → phone, port, app status/pid/restarts, desired state, release |
| `deployments` | one row per deploy (per phone) with status and timing |
| `deploy_logs` | streamed deploy output, pruned with its deployment |
| `alerts` | keyed alerts with severity, status (firing/ack/resolved) and timestamps |
| `settings` | key/value (alert thresholds, notification config) |
| `addons` | engine instance, pinned phone, protocol/bridge ports, storage hostname, data dir, encrypted credentials, status, **epoch** |
| `addon_placements` | addon → phone, role (`primary`/`standby`/`demoted`), pid/restarts, replay lag — sticky, never scheduled away |
| `addon_bindings` | addon ↔ project; drives environment injection |
| `addon_relays` | addon → app phone loopback facade port |
| `addon_logs` | provisioning, relay, backup, promotion and restore output (30-day retention) |
| `backups` | dump metadata, plaintext SHA-256, encryption flag and control-plane file path (last 10 per addon) |

Retention: raw samples for 14 days, deployment history for 30 days, each deployment keeps its log lines.
Metrics queries downsample to 30 s / 1 min / 5 min / 30 min buckets depending on the window, so the UI never loads
more than a few hundred points.

`UpsertServer` deliberately does **not** overwrite `name` or `labels` on reconnect: those are user-owned; the handler and the store both protect a rename from being clobbered by an agent hello.

## 6. Deploy pipeline

```
deploy button / API
  └─▶ placement: project → phone, port allocated from 4100–4999
        └─▶ agent executes a self-contained plan
              ├─ shallow git clone into ~/apps/<slug>/releases/<timestamp>
              ├─ build command (optional) with streamed output
              ├─ stop the previous process, verify the port is free
              ├─ start via supervisor with PORT + env
              ├─ health check (process alive + TCP + HTTP path)
              ├─ symlink current → release, keep 5 releases
              └─ on failure: restart the previous release, mark rolled_back
```

Source can be a git repository or an uploaded zip. Uploads are stored under `<data>/bundles/<project>.zip`; the
deploy request carries a `sourcePath`, and the agent downloads it from the authenticated
`/api/projects/{id}/source` endpoint using its agent token, then extracts it into the release directory. Uploads and
folder browser uploads use chunked `fs.write.chunk` calls (1 MB) so they stay under the WebSocket read limit, and
`fs.extract` rejects traversal, symlinks and archives expanding beyond 512 MB. This avoids requiring git on the phone
and works the same over LAN or Cloudflare.

Supervisor behaviour (`internal/agent/apps.go`):

- Build and runtime commands receive `NODE_ENV=production` and the project environment. Node projects that need
  `tsc`, Vite or other devDependencies during the build must use `npm install --include=dev` (or
  `npm ci --include=dev`); plain `npm ci` skips devDependencies under `NODE_ENV=production`.
- A single app project maps to one process and one port. A **stack** project groups child app projects, each with its
  own subdomain, port, process, health check and logs; the stack shares the repo, branch and encrypted env and deploys
  every service together. Services pinned to the same phone receive `PHLEET_SERVICE_<SLUG>_URL` loopback URLs.
- Each app is a process group (`Setpgid`), so stop/restart kills the whole tree.
- stdout/stderr go to `~/apps/<slug>/logs/app.log`, rotated at 5 MB (one old file kept).
- Unexpected exits are restarted with exponential backoff (1 s → 30 s); intentional stops are not.
- Wanted state lives in `~/.phleet/apps.json`, so an agent restart (or phone reboot via Termux:Boot) brings apps
  back without the control plane.
- Deploy-time exits do not trigger restart storms: the process is marked `deploying` and the deploy owns the outcome.
- Port ownership is verified with `/proc` only (no `lsof`/root). An orphaned process from a previous agent life is
  killed before a new release starts, and the health check aborts if the process exits, so a squatter can never make
  a broken release look healthy.

Placement is deliberately simple for 2–5 phones: the API picks the online phone with the lowest
`temp*2 + cpu/2` score (penalising low battery, favouring charging), or the selected phone, and allocates the first
free port. Deployment progress is pushed by the agent as events, persisted into `deployments`/`deploy_logs`, and
reconciled from heartbeats if the control plane missed a terminal event.

Isolation is directory-based (`~/apps/<slug>`), with optional `proot-distro` for glibc-heavy stacks. No cgroups
without root; use `nice`/`taskset` for rough fairness.

### 6.1 Addons

An addon is any supporting service beside projects — a database, cache, object storage, analytics or monitoring
process. It is a separate primitive, not a project: it has state, credentials and a lifecycle that a stateless deploy
must never touch. `internal/engines` is a platform-independent catalogue shared by the control plane and the agent.
Each entry renders:

| Piece | Purpose |
|---|---|
| `Init` | one-time data directory setup (`initdb`, write `redis.conf`, create a SQLite file, create the bucket dir) |
| `Install` | package install or a static-binary fallback (rclone) |
| `Run` | foreground command for the existing supervisor (never `pg_ctl start`, which escapes process-group tracking) |
| `Health` | engine-aware check (`pg_isready`, `redis-cli ping`, `sqlite3 quick_check`, rclone HTTP) |
| `PostStart` | idempotent user/database setup, run after health passes |
| `StandbyInit` / `Promote` / `Lag` | `pg_basebackup -R`, `pg_ctl promote`, `pg_stat_replication`; Redis `REPLICAOF` |
| `Backup` / `Restore` | engine-native dump and restore |
| `Env` | environment injected into bound projects |
| `Client` | one-shot console command |

Provisioning is a self-contained agent plan, like a deploy:

```
addon.create
  └─▶ control plane: allocate ports, generate credentials, encrypt, insert
        └─▶ agent: install (if missing)
              ├─ mkdir ~/phleet/data/<slug>, refuse a non-empty directory without a marker
              ├─ Init (once), write .phleet-addon sentinel
              ├─ start supervised process with engine ports/signals
              ├─ health check, then PostStart (idempotent)
              └─ status + log events
```

Four access paths, all over the existing outbound tunnel:

| Path | Mechanism |
|---|---|
| same phone | direct `127.0.0.1:<protocol port>` |
| app on another phone | loopback facade on that phone → `addon.relay` stream → control plane → primary agent |
| PC client | `tcpBridge` listens on `127.0.0.1:<bridge port>` and opens a `tcp` stream to the primary |
| S3 clients | `s3-<name>.<domain>` → storage router → `ProxyHTTPOpts` (streamed body) → rclone on the phone |

`addon_relays` stores one facade port per (addon, app phone). The relay stream is authorized by that row and resolves
the **current** primary on every connection, so promotion does not require redeploying apps. The bridge port is stored
on the addon and allocated from a separate range (`15400–16499`); relays use `16500–17499`.

Two rules protect data:

- **The sentinel gates initialisation.** A data directory without `.phleet-addon` that is not empty is refused. An
  addon whose phone is replaced is marked `needs_restore`; the reconciler never calls the normal replica path and
  never starts an empty database.
- **A database is sticky.** `addon_placements` is written once and only removed explicitly. Drain and failover do not
  move it: losing a phone means losing the volume, so the correct response is restore-from-backup or a manual promote.

Credentials are generated per addon (role password, superuser password and, for replication-capable engines, a
replication role), encrypted with the control plane key and never listed. Deploying a project that is bound to a
**SQLite** addon on a different phone is refused; PostgreSQL, Redis and S3 are relayed. A database-bound project can
therefore run multiple replicas, each using loopback (same phone) or a facade (other phones).

Backups are produced on the phone (`pg_dump -Fc`, `redis-cli --rdb`, SQLite `.backup`, a tar of the bucket), uploaded
in 32 MB chunks under `<data>/backups/<addon>/`, encrypted with chunked AES-256-GCM, checksummed with the plaintext
SHA-256, and pruned to ten. Restore verifies the checksum, writes a local pre-restore snapshot under the addon's
`backups/` directory, downloads to the **agent state dir** (not the data dir, which rclone's restore clears) and runs
the engine's restore path; Postgres restores through a live connection, Redis, SQLite and rclone are stopped first. A background loop takes a daily backup when the phone is
charging or above 40 %.

Replication is explicit: `POST /addons/{id}/standby` allocates a port and data directory on another phone, opens a
relay back to the primary, and runs `pg_basebackup`/`REPLICAOF`. The primary samples lag every 30 s into the
heartbeat; `POST /addons/{id}/promote` fences the old primary (`addon.control stop`) before `addon.promote`. There is
no quorum, so promotion is manual. Each promotion increments `addons.epoch`; the agent reports its epoch in every
heartbeat, and a lower epoch is fenced (stopped and marked demoted) when the phone reconnects. A demoted placement
can be removed with `DELETE /addons/{id}/placement/{serverId}` and re-added as a fresh standby.
Auto-failover is opt-in: the primary renews `addons.lease_until` on each heartbeat, the agent self-fences at
`leaseSec + failoverAfter - 30s`, and the reconciler promotes after `lease_until + failoverAfter` when a standby is
running and its last lag is within `max_lag_ms`. The control plane is the witness; a panel outage longer than the
lease is a database outage for opted-in addons.
See [runbooks/database-failover.md](runbooks/database-failover.md) for the procedure and the auto-failover trade-offs.


## 7. Scheduling, failover and alerts

### 7.1 Desired state

A project declares `replicas` (1–5), an optional label selector (`tier=web,region=home`) and an optional pinned
phone. Placements are rows; the scheduler never stores "which phone" anywhere else, so it can move work freely.

`internal/placement` is a pure package (unit-tested) that answers one question: given phone states, which phones
should receive the missing replicas? It filters to online, non-drained phones that match every selector key, then
orders by (existing placement count, health score, id). The health score is `temp*2 + cpu/2`, penalising low battery
and rewarding charging.

### 7.2 Reconciler

`reconcileLoop` runs every 30 seconds and is also triggered by agent connect/disconnect, deploy completion, label
changes, replica changes and `POST /api/rebalance`.

- adds replicas while `active < desired`;
- **waits until a replacement is running before pruning** a drained/offline/mismatched placement, so failover does
  not create avoidable downtime;
- prunes over-capacity placements (oldest kept);
- skips a project while a deployment is in flight, with a 2-minute cooldown after adding, so a failing build cannot
  loop;
- expires deployments stuck for 20 minutes (agent vanished mid-deploy) so scheduling unblocks.

### 7.3 Drain

Draining a phone marks it ineligible for new placements, removes it from routing, and lets the reconciler move its
apps. Once a replacement reports `running`, the old placement is stopped and deleted. A drained phone that comes back
does not receive traffic again until un-drained.

### 7.4 Routing and orphans

Requests are round-robined across `running` replicas; placements without a tunnel or with `desired=stopped` are
skipped. If a placement is removed while its phone is offline, the app keeps running on the device (agents are
autonomous). On the next heartbeat the control plane sees an app it no longer places and sends a delete, so orphans
do not linger.

### 7.5 Alerts

An evaluator runs every 30 seconds against servers, placements and the latest deployment:

| Kind | Default | Severity |
|---|---|---|
| offline | 120 s without heartbeat | critical |
| high temp | ≥ 60 °C (+10 = critical) | warning/critical |
| low battery | ≤ 15 %, not charging | warning |
| disk full | ≥ 90 % used (≥ 95 = critical) | warning/critical |
| app crashed | placement status `crashed` | warning |
| addon failed | addon status `failed` or `crashed` | critical |
| addon unreachable | phone online but the addon is not serving | warning |
| deploy failed | latest deployment `failed` | critical |

Alerts are keyed (`server:<id>:temp`, `deploy:<project>`, …) so they open once and update in place instead of
spamming. They resolve automatically when the condition clears, and can be acknowledged while still firing. History
is kept for 90 days. Thresholds are editable at runtime; notifications go to a Telegram bot and/or a generic webhook
(`{event, text, alert}`) with a test button in the UI. Notifications are best-effort and never block evaluation.

## 8. Networking and Cloudflare

- `setup-cloudflare` needs a token with `Zone:Read`, `DNS:Edit`, `Account:Cloudflare Tunnel:Edit`.
- DNS: `fleet.<domain>` and `*.<domain>` both CNAME to `<tunnel-id>.cfargotunnel.com`, proxied.
- Ingress: `fleet.<domain> → 127.0.0.1:8080`, then a catch-all to the same place. The control plane decides.
- Project URLs use **first-level** subdomains (`api.example.com`) because Universal SSL covers `example.com` and `*.example.com`, not `*.apps.example.com`. Advanced Certificate Manager removes that constraint later.
- Cloudflare free-tier limits that matter: 100 MB per request (uploads are capped at 32 MB; builds happen on-device), ~100 s idle timeout (WebSocket traffic and 5 s heartbeats keep sessions alive), and ToS discouragement of heavy media proxying.
- `phleetd` reloads `cloudflare.json` every 15 s, so setup takes effect without a restart.
- **Raw TCP needs a client helper.** A proxied hostname terminates HTTP. Cloudflare Tunnel can stream arbitrary TCP
  only when the client also runs `cloudflared access tcp` (with a Cloudflare Access application), or through Spectrum
  (Enterprise). There is no plain `db.example.com:5432`. phleet therefore does not publish databases: loopback for
  same-phone apps, an automatic relay for other phones, and a control-plane bridge for the PC.
- **The bridge is a loopback listener** (`127.0.0.1:15400+`) that opens a `tcp` yamux stream to the primary. It needs
  no DNS or tunnel change. `-bridge-lan` binds `0.0.0.0` instead; the engine still requires its password.
- **Relays are agent-initiated.** An app phone's agent opens a `relay` stream over its existing data tunnel, the
  control plane validates the `addon_relays` row, and bridges it to the primary. This is the same WebSocket the agent
  already keeps open, so no inbound port, DNS record or Cloudflare feature is involved.
- **S3 is the HTTP exception.** Object storage gets `s3-<addon>.<domain>` (covered by the wildcard) and is routed
  before the project lookup. Request bodies stream straight through; the cap is 96 MB per request, so clients use
  multipart for larger objects. MinIO's community binaries were retired in 2025, so the engine runs `rclone serve s3`
  over a normal directory.

## 9. Security model

| Boundary | Control |
|---|---|
| Public → panel | Cloudflare Access (recommended) + token login with signed, HttpOnly, SameSite=Lax cookie (30 days) |
| Agent → control plane | the shared token enrolls a phone once; the control plane then issues a per-device token (stored as a SHA-256 hash) that the agent uses for control, data, source and backup connections. Tokens are compared in constant time, are never accepted by the panel API, and are passed by file (`--token-file`) rather than argv |
| Panel → files | paths resolved with `EvalSymlinks` and checked against allowed roots on every operation; root directories cannot be deleted |
| Panel → shell | full shell as the Termux user, by design; the UI is the trust boundary |
| Host header | after setup, unknown public hostnames return 404 instead of the panel; loopback and private IPs stay allowed; known project hosts are proxied before the panel mux |
| Panel → apps | only allocated ports are reachable; the session cookie and panel bearer tokens are stripped; hop-by-hop headers removed |
| Panel → databases | engine binds `127.0.0.1` only; passwords are SCRAM/`requirepass`, generated per addon and encrypted at rest; the console runs as the Termux user behind the admin session |
| Agent → addon files | the agent rejects any data directory outside `$HOME/phleet/data`; a purge requires an explicit flag from the control plane |
| PC → database bridge | binds `127.0.0.1` by default; `-bridge-lan` is an explicit opt-in and the engine still requires its password |
| Phone → phone relay | the control plane only forwards a relay stream when an `addon_relays` row authorizes that addon on that phone, and the target is the current primary's protocol port |
| Source bundles | uploaded zips live under `%ProgramData%\phleet\bundles` and are served only to authenticated agents/admins; they are not encrypted at rest |
| Addon backups | dumps live under `%ProgramData%\phleet\backups`, are encrypted at rest with chunked AES-256-GCM, carry a plaintext SHA-256, and are removed with the addon |
| Data at rest | SQLite and tokens under `%ProgramData%\phleet`, outside OneDrive; project env encrypted with AES-256-GCM; tokens are file-permission protected |
| Audit | connection, exec, file write/upload/delete, terminal, project create/delete and deploy lifecycle events are recorded |

Known gaps (acceptable for a personal fleet, not for a team): the enrollment token is still shared (but cannot re-enroll an existing device), no per-user accounts or RBAC, no per-session CSRF token (state-changing requests are Origin-checked and JSON endpoints require `Content-Type: application/json`, which forces a preflight), no per-project secrets scoping beyond the env map. Apps run as the same Unix user as the agent on Termux, so stronger workload isolation is still on the roadmap.

## 10. Failure model

| Failure | Behaviour |
|---|---|
| Windows PC sleeps | Public URLs and the panel disappear. Agents keep running and supervising apps; metrics and logs buffer briefly on the device and are dropped beyond a cap. On wake, agents reconnect with jitter and the UI shows gaps, not fake zeros. |
| Agent crashes | Termux service supervisor (`runit`) or the boot hook restarts it; the panel shows the phone offline after 45 s. Supervised apps keep running and are re-adopted (or replaced) on restart; a port still held by the old process is detected and that orphan is killed before a new release starts. |
| Phone reboots | Termux:Boot hook restarts the agent; apps are restored from `~/.phleet/apps.json`; the server row is reused by stable agent id. |
| Deploy health fails | The new release is stopped, the previous release is started and health-checked, and the deployment is marked `rolled_back`. |
| Agent exits mid-deploy | The deploy continues to completion or rollback because the plan is self-contained; the panel reconciles the terminal state from the next heartbeat (`apps[].deployStatus`). |
| Phone offline mid-request | Pending requests fail fast with `server is offline`; the UI surfaces the error. |
| Phone lost with replicas | Traffic shifts to running replicas immediately; the reconciler adds a replacement on an eligible phone and only then removes the stale placement. With `replicas=1` there is downtime until the replacement is healthy. |
| Drained phone | Removed from routing and new placements; apps are migrated and then stopped. |
| Alert storm | Alerts are keyed and deduplicated; one row per condition, updated in place, with automatic resolution. |
| Addon phone offline | The addon is marked `unreachable`; bound apps get connection errors; **no automatic re-placement** because the volume is not there. Restore from backup or promote a standby by hand. |
| Addon volume lost / phone replaced | The marker is missing, so provisioning refuses to initialise. The addon needs an explicit restore; it never comes back empty. |
| Agent dies during `initdb` | The sentinel is written only after init succeeds; a failed init removes the fresh directory and the reconciler retries the whole plan. |
| Postgres killed mid-write | WAL recovery on the next supervised start; Redis replays AOF; SQLite WAL rolls back. Data is safe, startup is slower. |
| Addon bound to another phone | For SQLite the deploy is refused. For PostgreSQL, Redis and S3 the control plane allocates a loopback facade, instructs the agent, and injects the facade URL instead. |
| Bridge port occupied | The listener fails to bind and is retried on the 30 s reconcile tick; the addon itself is unaffected and remains reachable through the panel console and relays. |
| Standby lag / promotion | Lag is sampled every 30 s. Promote is refused unless the standby is `running`; the old primary is stopped first. If it is offline, it is marked `demoted` and stopped when it reappears. |
| Stale primary reconnects | Its heartbeat carries the old epoch; the control plane stops it and marks the placement demoted. It can only return as a fresh standby. |
| Auto-failover lease expires | The agent self-fences the primary; after `failoverAfter` the reconciler promotes a running standby within the lag bound and advances the epoch. A panel outage longer than the lease stops opted-in primaries by design. |
| Demoted copy removed | `DELETE /addons/{id}/placement/{serverId}` stops the process, purges its data and removes the row, so the phone can be re-added as a standby. |
| Whole request > 96 MB to S3 | The proxy returns 413. S3 clients should use multipart uploads (default for large objects). |
| Control plane restarts | All agents reconnect within seconds; `servers` are reset to offline at boot to avoid stale "online" badges. |
| Clock skew | Timestamps come from the control plane for storage; agent heartbeat time is advisory. |

## 11. Android constraints and mitigations

| Constraint | Mitigation |
|---|---|
| Doze / app standby kills Termux | wake lock, battery "Unrestricted", foreground service via runit |
| Phantom process killer (Android 12+) | one-time ADB `device_config put activity_manager max_phantom_processes 2147483647` |
| No containers without root | per-app directories, optional `proot-distro`; native runtimes only |
| Thermal throttling | temperature in every heartbeat; placement favours cool phones |
| Battery wear at 100 % | charge-control badge when rooted; otherwise operational guidance |
| eMMC wear | bounded app logs (5 MB), 14-day metrics, five releases per app, 30-day deploy history |
| Python native deps on Android | prefer Termux packages, or run the app inside `proot-distro` Debian |
| Database memory footprint | one engine instance per phone, `shared_buffers=64MB`, `max_connections=40`; the scheduler refuses to attach a database to a multi-replica project |
| eMMC wear from writes | Postgres keeps `fsync=on`; five releases and bounded logs; backups are off-device by design |
| Engine package installs need network | the bootstrap accepts `--with postgres,redis,sqlite,rclone`; otherwise the agent installs on first use and streams the log |
| Object storage on a phone | `rclone serve s3` over a plain directory; the backup is a tar of the bucket, not an opaque volume |
| No quorum for database failover | promotion is manual and fences the old primary; automation waits for a witness/quorum design |
| No `/etc/resolv.conf` | the agent resolves hostnames with DNS-over-HTTPS (1.1.1.1, then 8.8.8.8) and caches answers for 5 minutes |
| No system CA bundle for Go | the agent sets `SSL_CERT_FILE` to `$PREFIX/etc/tls/cert.pem` if the variable is unset |
| Android 10 seccomp blocks `faccessat2` | executable lookup uses `os.Stat` instead of `exec.LookPath`; never call `exec.LookPath` in agent code |
| Android 10 seccomp blocks `pidfd_open` | the arm64 agent is built with Go 1.22, which predates the pidfd probe used by `os.StartProcess` |
| `/proc/stat`, `/proc/net`, thermal and battery sysfs hidden | CPU/network/thermal/battery telemetry is unavailable without root; Termux:API provides battery data |

## 12. Decisions log

- **Go for both sides** — one static binary per role, first-class PTY/WebSocket/SSH support, trivial Windows service packaging via Task Scheduler.
- **SQLite, not Postgres** — the target fleet is 2–5 phones; a single WAL file is faster to operate and easy to back up.
- **Agent-owned state** — the control plane is a view and a command channel, not a single point of execution.
- **Cloudflare Tunnel over Tailscale** — no client software on the phones beyond the agent, public TLS handled at the edge, wildcard routing for free.
- **No SSH data plane** — avoids NAT, quoting and per-request overhead; SSH remains the bootstrap mechanism.
- **yamux over a second WebSocket** — one stream per request, no per-request TCP handshake, works through Cloudflare without new DNS or tunnel rules.
- **Self-contained deploy plans** — the agent owns execution and health checks, so a sleeping panel cannot corrupt a release.
- **Pure scheduler** — placement rules are a small, unit-tested package with no database or network access, which keeps failover behaviour predictable.
- **Alerts live in the control plane** — agents should not depend on notification credentials, and the control plane already has the fleet view.
- **Addons are a separate primitive** — reusing `projects` would let the stateless deploy and failover paths touch a
  volume. Addons reuse the supervisor and heartbeat but get their own placement and reconcile rules.
- **Loopback-first databases** — the cheapest correct network is none. Apps and their database share a phone when possible; the relay and bridge are additive.
- **Never recreate an empty database** — the sentinel plus the pinned placement makes "phone lost" a restore decision, not a scheduling decision. Automatic promotion waits for a quorum design.
- **rclone for object storage** — one maintained static binary, Termux-packaged, S3 over a plain directory. MinIO's community binaries disappeared in 2025; a source build is too heavy for a phone.
- **Manual promotion** — two phones have no quorum. Fencing the old primary before promoting is the only safe default.
- **Epoch fencing** — every promotion increments a durable epoch; the agent reports it, and a stale primary is stopped when it reconnects.
- **Opt-in lease auto-failover** — automatic promotion needs self-fencing. The primary renews a lease; the agent stops the engine when the panel is unreachable; only then does the control plane promote. Off by default because it trades a panel outage for a database outage.
- **Live engine tests** — the rendered commands are exercised against real PostgreSQL and Redis (portable, no root). This is how the `initdb` password-file, standby quoting, data-dir permission, replication-credential and promotion-readiness bugs were found before a phone saw them.

## 13. Roadmap

1. Data: bucket mirroring between two phones; per-addon quotas; active engine health probes for hung processes.
2. Per-device tokens, API tokens and RBAC, audit export.
3. Build caching, scheduled jobs, restore drills, mobile-friendly panel polish.
