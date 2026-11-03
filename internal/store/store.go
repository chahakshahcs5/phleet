// Package store persists control-plane state in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"phleet/internal/model"
)

type Store struct {
	db *sql.DB
}

var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS servers (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  hostname      TEXT NOT NULL DEFAULT '',
  os            TEXT NOT NULL DEFAULT '',
  arch          TEXT NOT NULL DEFAULT '',
  agent_version TEXT NOT NULL DEFAULT '',
  caps          TEXT NOT NULL DEFAULT '{}',
  labels        TEXT NOT NULL DEFAULT '{}',
  status        TEXT NOT NULL DEFAULT 'offline',
  drain         INTEGER NOT NULL DEFAULT 0,
  device_token_hash TEXT NOT NULL DEFAULT '',
  last_seen     INTEGER NOT NULL DEFAULT 0,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS metrics (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id   TEXT NOT NULL,
  ts          INTEGER NOT NULL,
  cpu         REAL NOT NULL DEFAULT 0,
  mem_used    INTEGER NOT NULL DEFAULT 0,
  mem_total   INTEGER NOT NULL DEFAULT 0,
  disk_used   INTEGER NOT NULL DEFAULT 0,
  disk_total  INTEGER NOT NULL DEFAULT 0,
  temp_c      REAL NOT NULL DEFAULT 0,
  battery_pct INTEGER NOT NULL DEFAULT -1,
  battery_temp REAL NOT NULL DEFAULT 0,
  charging    INTEGER NOT NULL DEFAULT 0,
  load1       REAL NOT NULL DEFAULT 0,
  net_rx      INTEGER NOT NULL DEFAULT 0,
  net_tx      INTEGER NOT NULL DEFAULT 0,
  uptime      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_metrics_server_ts ON metrics(server_id, ts);
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics(ts);

CREATE TABLE IF NOT EXISTS events (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  ts     INTEGER NOT NULL,
  actor  TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL DEFAULT '',
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts);
CREATE INDEX IF NOT EXISTS idx_events_action_id ON events(action, id DESC);

CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  slug        TEXT NOT NULL UNIQUE,
  repo        TEXT NOT NULL DEFAULT '',
  branch      TEXT NOT NULL DEFAULT 'main',
  runtime     TEXT NOT NULL DEFAULT '',
  build_cmd   TEXT NOT NULL DEFAULT '',
  start_cmd   TEXT NOT NULL DEFAULT '',
  health_path TEXT NOT NULL DEFAULT '/',
  hostname    TEXT NOT NULL DEFAULT '',
  env_enc     TEXT NOT NULL DEFAULT '',
  selector    TEXT NOT NULL DEFAULT '',
  replicas    INTEGER NOT NULL DEFAULT 1,
  pinned_server TEXT NOT NULL DEFAULT '',
  source_type TEXT NOT NULL DEFAULT 'git',
  source_size INTEGER NOT NULL DEFAULT 0,
  source_updated_at INTEGER NOT NULL DEFAULT 0,
  parent_id   TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'app',
  stack_order INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_projects_hostname ON projects(hostname);

CREATE TABLE IF NOT EXISTS placements (
  project_id TEXT NOT NULL,
  server_id  TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  desired    TEXT NOT NULL DEFAULT 'running',
  port       INTEGER NOT NULL DEFAULT 0,
  pid        INTEGER NOT NULL DEFAULT 0,
  restarts   INTEGER NOT NULL DEFAULT 0,
  release    TEXT NOT NULL DEFAULT '',
  last_error TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (project_id, server_id)
);

CREATE TABLE IF NOT EXISTS deployments (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  server_id   TEXT NOT NULL,
  commit_sha  TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'queued',
  release     TEXT NOT NULL DEFAULT '',
  started_at  INTEGER NOT NULL DEFAULT 0,
  finished_at INTEGER NOT NULL DEFAULT 0,
  detail      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id, started_at);

CREATE TABLE IF NOT EXISTS deploy_logs (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  deployment_id TEXT NOT NULL,
  ts            INTEGER NOT NULL,
  line          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deploy_logs ON deploy_logs(deployment_id, id);

CREATE TABLE IF NOT EXISTS alerts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  key         TEXT NOT NULL UNIQUE,
  kind        TEXT NOT NULL,
  severity    TEXT NOT NULL DEFAULT 'warning',
  status      TEXT NOT NULL DEFAULT 'firing',
  target      TEXT NOT NULL DEFAULT '',
  message     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  resolved_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_alerts_status ON alerts(status, updated_at);

CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS addons (
  id               TEXT PRIMARY KEY,
  name             TEXT NOT NULL,
  slug             TEXT NOT NULL UNIQUE,
  engine           TEXT NOT NULL,
  kind             TEXT NOT NULL DEFAULT 'database',
  owner_project_id TEXT NOT NULL DEFAULT '',
  pinned_server    TEXT NOT NULL DEFAULT '',
  port             INTEGER NOT NULL DEFAULT 0,
  bridge_port      INTEGER NOT NULL DEFAULT 0,
  hostname         TEXT NOT NULL DEFAULT '',
  epoch            INTEGER NOT NULL DEFAULT 1,
  data_dir         TEXT NOT NULL DEFAULT '',
  size_limit_mb    INTEGER NOT NULL DEFAULT 0,
  replication_mode TEXT NOT NULL DEFAULT 'none',
  auto_failover    INTEGER NOT NULL DEFAULT 0,
  failover_after_sec INTEGER NOT NULL DEFAULT 120,
  max_lag_ms       INTEGER NOT NULL DEFAULT 30000,
  lease_sec        INTEGER NOT NULL DEFAULT 60,
  lease_until      INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT 'pending',
  last_error       TEXT NOT NULL DEFAULT '',
  env_enc          TEXT NOT NULL DEFAULT '',
  config_enc       TEXT NOT NULL DEFAULT '',
  service          INTEGER NOT NULL DEFAULT 0,
  app_id           TEXT NOT NULL DEFAULT '',
  version          TEXT NOT NULL DEFAULT '',
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_addons_owner ON addons(owner_project_id);
CREATE INDEX IF NOT EXISTS idx_addons_hostname ON addons(hostname);

CREATE TABLE IF NOT EXISTS addon_placements (
  addon_id   TEXT NOT NULL,
  server_id  TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'primary',
  status     TEXT NOT NULL DEFAULT 'pending',
  desired    TEXT NOT NULL DEFAULT 'running',
  port       INTEGER NOT NULL DEFAULT 0,
  pid        INTEGER NOT NULL DEFAULT 0,
  restarts   INTEGER NOT NULL DEFAULT 0,
  lag_ms     INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (addon_id, server_id)
);

CREATE TABLE IF NOT EXISTS addon_relays (
  addon_id   TEXT NOT NULL,
  server_id  TEXT NOT NULL,
  port       INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (addon_id, server_id)
);
CREATE INDEX IF NOT EXISTS idx_addon_relays_server ON addon_relays(server_id);

CREATE TABLE IF NOT EXISTS addon_bindings (
  addon_id   TEXT NOT NULL,
  project_id TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (addon_id, project_id)
);
CREATE INDEX IF NOT EXISTS idx_addon_bindings_project ON addon_bindings(project_id);

CREATE TABLE IF NOT EXISTS addon_logs (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  addon_id TEXT NOT NULL,
  ts       INTEGER NOT NULL,
  line     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_addon_logs ON addon_logs(addon_id, id);

CREATE TABLE IF NOT EXISTS backups (
  id          TEXT PRIMARY KEY,
  addon_id    TEXT NOT NULL,
  server_id   TEXT NOT NULL DEFAULT '',
  kind        TEXT NOT NULL DEFAULT 'manual',
  status      TEXT NOT NULL DEFAULT 'running',
  size        INTEGER NOT NULL DEFAULT 0,
  checksum    TEXT NOT NULL DEFAULT '',
  path        TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_backups_addon ON backups(addon_id, created_at DESC);
`

func Open(path string) (*Store, error) {
	dsn := path
	if path != ":memory:" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return nil, err
		}
		// Build a proper file:/// URI. On Windows the drive letter must not be
		// parsed as a URI authority, so the slash-prefixed path is required.
		p := filepath.ToSlash(abs)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		u := url.URL{Scheme: "file", Path: p}
		q := u.Query()
		q.Add("_pragma", "busy_timeout(5000)")
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Add("_pragma", "foreign_keys(1)")
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	// Migrate phase-1 databases in place.
	migrations := []struct{ table, column, decl string }{
		{"projects", "env_enc", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "selector", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "replicas", "INTEGER NOT NULL DEFAULT 1"},
		{"projects", "pinned_server", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "parent_id", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "kind", "TEXT NOT NULL DEFAULT 'app'"},
		{"projects", "stack_order", "INTEGER NOT NULL DEFAULT 0"},
		{"placements", "pid", "INTEGER NOT NULL DEFAULT 0"},
		{"placements", "restarts", "INTEGER NOT NULL DEFAULT 0"},
		{"placements", "release", "TEXT NOT NULL DEFAULT ''"},
		{"placements", "last_error", "TEXT NOT NULL DEFAULT ''"},
		{"placements", "updated_at", "INTEGER NOT NULL DEFAULT 0"},
		{"placements", "desired", "TEXT NOT NULL DEFAULT 'running'"},
		{"deployments", "release", "TEXT NOT NULL DEFAULT ''"},
		{"servers", "drain", "INTEGER NOT NULL DEFAULT 0"},
		{"servers", "device_token_hash", "TEXT NOT NULL DEFAULT ''"},
		{"projects", "source_type", "TEXT NOT NULL DEFAULT 'git'"},
		{"projects", "source_size", "INTEGER NOT NULL DEFAULT 0"},
		{"projects", "source_updated_at", "INTEGER NOT NULL DEFAULT 0"},
		{"addons", "bridge_port", "INTEGER NOT NULL DEFAULT 0"},
		{"addons", "hostname", "TEXT NOT NULL DEFAULT ''"},
		{"addons", "epoch", "INTEGER NOT NULL DEFAULT 1"},
		{"addons", "auto_failover", "INTEGER NOT NULL DEFAULT 0"},
		{"addons", "failover_after_sec", "INTEGER NOT NULL DEFAULT 120"},
		{"addons", "max_lag_ms", "INTEGER NOT NULL DEFAULT 30000"},
		{"addons", "lease_sec", "INTEGER NOT NULL DEFAULT 60"},
		{"addons", "lease_until", "INTEGER NOT NULL DEFAULT 0"},
		{"addons", "config_enc", "TEXT NOT NULL DEFAULT ''"},
		{"addons", "service", "INTEGER NOT NULL DEFAULT 0"},
		{"addons", "app_id", "TEXT NOT NULL DEFAULT ''"},
		{"addons", "version", "TEXT NOT NULL DEFAULT ''"},
		{"addon_placements", "lag_ms", "INTEGER NOT NULL DEFAULT 0"},
		{"backups", "checksum", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, m := range migrations {
		if err := ensureColumn(db, m.table, m.column, m.decl); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate %s.%s: %w", m.table, m.column, err)
		}
	}
	// Indexes on columns added by migrations must be created after the ALTERs.
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_projects_parent ON projects(parent_id, stack_order)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate index projects.parent: %w", err)
	}
	return &Store{db: db}, nil
}

func ensureColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + decl)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

// Backup writes a consistent snapshot of the database using SQLite's VACUUM
// INTO. It is safe to run against the live database; WAL readers still see a
// coherent state.
func (s *Store) Backup(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)
	// VACUUM INTO does not accept a bound parameter on all SQLite builds.
	quoted := "'" + strings.ReplaceAll(path, "'", "''") + "'"
	_, err := s.db.Exec(`VACUUM INTO ` + quoted)
	return err
}

func now() int64 { return time.Now().Unix() }

func jsonMap(v any) string {
	b, _ := json.Marshal(v)
	if len(b) == 0 {
		return "{}"
	}
	return string(b)
}

func (s *Store) UpsertServer(srv model.Server) error {
	ts := now()
	if srv.CreatedAt == 0 {
		srv.CreatedAt = ts
	}
	_, err := s.db.Exec(`
INSERT INTO servers (id,name,hostname,os,arch,agent_version,caps,labels,status,drain,last_seen,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,0,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  hostname=excluded.hostname, os=excluded.os, arch=excluded.arch,
  agent_version=excluded.agent_version, caps=excluded.caps, status=excluded.status,
  last_seen=excluded.last_seen, updated_at=excluded.updated_at`,
		srv.ID, srv.Name, srv.Hostname, srv.OS, srv.Arch, srv.AgentVersion,
		jsonMap(srv.Caps), jsonMap(srv.Labels), srv.Status, srv.LastSeen, srv.CreatedAt, ts)
	return err
}

func (s *Store) SetServerCaps(id string, caps map[string]any) error {
	_, err := s.db.Exec(`UPDATE servers SET caps=?, updated_at=? WHERE id=?`, jsonMap(caps), now(), id)
	return err
}

func (s *Store) TouchServer(id, status string) error {
	_, err := s.db.Exec(`UPDATE servers SET status=?, last_seen=?, updated_at=? WHERE id=?`,
		status, now(), now(), id)
	return err
}

func (s *Store) SetServerOffline(id string) error {
	_, err := s.db.Exec(`UPDATE servers SET status='offline', updated_at=? WHERE id=?`, now(), id)
	return err
}

func (s *Store) MarkStaleOffline(cutoff int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE servers SET status='offline', updated_at=? WHERE status!='offline' AND last_seen < ?`, now(), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResetServersOffline is called at boot: nothing is connected yet.
func (s *Store) ResetServersOffline() error {
	_, err := s.db.Exec(`UPDATE servers SET status='offline', updated_at=?`, now())
	return err
}

func (s *Store) UpdateServerMeta(id, name string, labels map[string]string) error {
	_, err := s.db.Exec(`UPDATE servers SET name=?, labels=?, updated_at=? WHERE id=?`, name, jsonMap(labels), now(), id)
	return err
}

// GetServerDeviceHash returns the hash of the per-device agent token. An empty
// hash means the phone is still using the shared enrollment token.
func (s *Store) GetServerDeviceHash(id string) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT device_token_hash FROM servers WHERE id=?`, id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (s *Store) SetServerDeviceHash(id, hash string) error {
	res, err := s.db.Exec(`UPDATE servers SET device_token_hash=?, updated_at=? WHERE id=?`, hash, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetServerDrain(id string, drain bool) error {
	v := 0
	if drain {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE servers SET drain=?, updated_at=? WHERE id=?`, v, now(), id)
	return err
}

func (s *Store) DeleteServer(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM placements WHERE server_id=?`,
		`DELETE FROM addon_relays WHERE server_id=?`,
		`DELETE FROM metrics WHERE server_id=?`,
		`DELETE FROM servers WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListServers() ([]model.Server, error) {
	rows, err := s.db.Query(`SELECT id,name,hostname,os,arch,agent_version,caps,labels,status,drain,last_seen,created_at,updated_at FROM servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Server{}
	for rows.Next() {
		var srv model.Server
		var caps, labels string
		var drain int
		if err := rows.Scan(&srv.ID, &srv.Name, &srv.Hostname, &srv.OS, &srv.Arch, &srv.AgentVersion,
			&caps, &labels, &srv.Status, &drain, &srv.LastSeen, &srv.CreatedAt, &srv.UpdatedAt); err != nil {
			return nil, err
		}
		srv.Drain = drain != 0
		_ = json.Unmarshal([]byte(caps), &srv.Caps)
		_ = json.Unmarshal([]byte(labels), &srv.Labels)
		if srv.Caps == nil {
			srv.Caps = map[string]any{}
		}
		if srv.Labels == nil {
			srv.Labels = map[string]string{}
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

func (s *Store) GetServer(id string) (model.Server, error) {
	var srv model.Server
	var caps, labels string
	var drain int
	err := s.db.QueryRow(`SELECT id,name,hostname,os,arch,agent_version,caps,labels,status,drain,last_seen,created_at,updated_at FROM servers WHERE id=?`, id).
		Scan(&srv.ID, &srv.Name, &srv.Hostname, &srv.OS, &srv.Arch, &srv.AgentVersion,
			&caps, &labels, &srv.Status, &drain, &srv.LastSeen, &srv.CreatedAt, &srv.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return srv, ErrNotFound
	}
	if err != nil {
		return srv, err
	}
	srv.Drain = drain != 0
	_ = json.Unmarshal([]byte(caps), &srv.Caps)
	_ = json.Unmarshal([]byte(labels), &srv.Labels)
	if srv.Caps == nil {
		srv.Caps = map[string]any{}
	}
	if srv.Labels == nil {
		srv.Labels = map[string]string{}
	}
	return srv, nil
}

func (s *Store) InsertMetrics(serverID string, m model.Metrics) error {
	if m.At == 0 {
		m.At = now()
	}
	charging := 0
	if m.Charging {
		charging = 1
	}
	_, err := s.db.Exec(`INSERT INTO metrics
(server_id,ts,cpu,mem_used,mem_total,disk_used,disk_total,temp_c,battery_pct,battery_temp,charging,load1,net_rx,net_tx,uptime)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		serverID, m.At, m.CPUPercent, m.MemUsed, m.MemTotal, m.DiskUsed, m.DiskTotal,
		m.TempC, m.BatteryPct, m.BatteryTemp, charging, m.Load1, m.NetRx, m.NetTx, m.Uptime)
	return err
}

// QueryMetrics returns samples newer than since, downsampled to bucketSec.
func (s *Store) QueryMetrics(serverID string, since int64, bucketSec int64) ([]model.Sample, error) {
	if bucketSec < 1 {
		bucketSec = 1
	}
	rows, err := s.db.Query(`
SELECT (ts/?)*? AS b,
       AVG(cpu), AVG(mem_used), MAX(mem_total), AVG(disk_used), MAX(disk_total),
       AVG(temp_c), AVG(battery_pct), AVG(battery_temp), MAX(charging),
       AVG(load1), MAX(net_rx), MAX(net_tx), MAX(uptime)
FROM metrics WHERE server_id=? AND ts>=? GROUP BY b ORDER BY b`, bucketSec, bucketSec, serverID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Sample{}
	for rows.Next() {
		var x model.Sample
		var charging int
		// AVG() always returns a REAL, even for integer columns. Scanning that
		// straight into int64 fails as soon as the average is fractional or
		// large enough to be rendered in exponent notation ("1.4e+09"), which
		// used to make this endpoint return 500 for any phone with real data.
		var memUsed, memTotal, diskUsed, diskTotal, batteryPct, netRx, netTx, uptime float64
		if err := rows.Scan(&x.TS, &x.CPUPercent, &memUsed, &memTotal, &diskUsed, &diskTotal,
			&x.TempC, &batteryPct, &x.BatteryTemp, &charging, &x.Load1, &netRx, &netTx, &uptime); err != nil {
			return nil, err
		}
		x.MemUsed = int64(math.Round(memUsed))
		x.MemTotal = int64(math.Round(memTotal))
		x.DiskUsed = int64(math.Round(diskUsed))
		x.DiskTotal = int64(math.Round(diskTotal))
		x.BatteryPct = int(math.Round(batteryPct))
		x.NetRx = int64(math.Round(netRx))
		x.NetTx = int64(math.Round(netTx))
		x.Uptime = int64(math.Round(uptime))
		x.At = x.TS
		x.Charging = charging != 0
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) LatestMetrics(serverID string) (model.Sample, error) {
	var x model.Sample
	var charging int
	err := s.db.QueryRow(`SELECT ts,cpu,mem_used,mem_total,disk_used,disk_total,temp_c,battery_pct,battery_temp,charging,load1,net_rx,net_tx,uptime
FROM metrics WHERE server_id=? ORDER BY ts DESC LIMIT 1`, serverID).
		Scan(&x.TS, &x.CPUPercent, &x.MemUsed, &x.MemTotal, &x.DiskUsed, &x.DiskTotal,
			&x.TempC, &x.BatteryPct, &x.BatteryTemp, &charging, &x.Load1, &x.NetRx, &x.NetTx, &x.Uptime)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	x.At = x.TS
	x.Charging = charging != 0
	return x, err
}

func (s *Store) PruneMetrics(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM metrics WHERE ts < ?`, cutoff)
	return err
}

func (s *Store) AddEvent(actor, action, target, detail string) error {
	// Audit rows are user-visible and searchable; keep them bounded. Exec
	// commands and error strings can be large, so trim here rather than in
	// every caller.
	const maxDetail = 2000
	if len(detail) > maxDetail {
		detail = detail[:maxDetail] + "…"
	}
	_, err := s.db.Exec(`INSERT INTO events (ts,actor,action,target,detail) VALUES (?,?,?,?,?)`,
		now(), actor, action, target, detail)
	return err
}

// PruneEvents keeps the audit log bounded. Metrics and deployment history are
// pruned separately; without this the events table grows forever.
func (s *Store) PruneEvents(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM events WHERE ts < ?`, cutoff)
	return err
}

func (s *Store) ListEvents(limit int) ([]model.Event, error) {
	evts, _, err := s.ListEventsPage(EventQuery{Limit: limit})
	return evts, err
}

// EventQuery describes one page of the audit log. Before is an exclusive
// keyset cursor (event id), which keeps pages stable while new events arrive.
type EventQuery struct {
	Limit  int
	Before int64
	Action string
	Actor  string
	Search string
}

func normalizeEventQuery(q EventQuery) EventQuery {
	if q.Limit <= 0 {
		q.Limit = 50
	}
	if q.Limit > 200 {
		q.Limit = 200
	}
	q.Action = strings.TrimSpace(q.Action)
	q.Actor = strings.TrimSpace(q.Actor)
	q.Search = strings.TrimSpace(q.Search)
	if len(q.Search) > 120 {
		q.Search = q.Search[:120]
	}
	return q
}

func (q EventQuery) where() (string, []any) {
	conds := make([]string, 0, 5)
	args := make([]any, 0, 8)
	if q.Before > 0 {
		conds = append(conds, "id < ?")
		args = append(args, q.Before)
	}
	if q.Action != "" {
		conds = append(conds, "action = ?")
		args = append(args, q.Action)
	}
	if q.Actor != "" {
		conds = append(conds, "actor = ?")
		args = append(args, q.Actor)
	}
	if q.Search != "" {
		conds = append(conds,
			"(instr(lower(action), lower(?)) > 0 OR instr(lower(target), lower(?)) > 0 OR instr(lower(detail), lower(?)) > 0)")
		args = append(args, q.Search, q.Search, q.Search)
	}
	if len(conds) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// ListEventsPage returns newest-first events plus whether an older page exists.
func (s *Store) ListEventsPage(q EventQuery) ([]model.Event, bool, error) {
	q = normalizeEventQuery(q)
	where, args := q.where()
	args = append(args, q.Limit+1)
	rows, err := s.db.Query(`SELECT id,ts,actor,action,target,detail FROM events `+where+` ORDER BY id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []model.Event{}
	for rows.Next() {
		var e model.Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(out) > q.Limit
	if hasMore {
		out = out[:q.Limit]
	}
	return out, hasMore, nil
}

// CountEvents counts matching events regardless of the page cursor.
func (s *Store) CountEvents(q EventQuery) (int, error) {
	q = normalizeEventQuery(q)
	q.Before = 0
	where, args := q.where()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM events `+where, args...).Scan(&n)
	return n, err
}

// EventFacets lists the distinct actions and actors used by the audit log.
func (s *Store) EventFacets() (actions, actors []string, err error) {
	rows, err := s.db.Query(`SELECT DISTINCT action FROM events WHERE action <> '' ORDER BY action`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, nil, err
		}
		actions = append(actions, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()

	rows, err = s.db.Query(`SELECT DISTINCT actor FROM events WHERE actor <> '' ORDER BY actor`)
	if err != nil {
		return actions, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return actions, nil, err
		}
		actors = append(actors, v)
	}
	return actions, actors, rows.Err()
}

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) Stats() (servers int, samples int, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM servers`).Scan(&servers); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM metrics`).Scan(&samples)
	return
}
