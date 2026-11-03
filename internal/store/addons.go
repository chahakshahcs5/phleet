package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

const addonCols = `id,name,slug,engine,kind,owner_project_id,pinned_server,port,data_dir,size_limit_mb,replication_mode,status,last_error,env_enc,config_enc,service,app_id,version,created_at,updated_at,hostname,bridge_port,epoch,auto_failover,failover_after_sec,max_lag_ms,lease_sec,lease_until`

func (s *Store) scanAddon(row interface{ Scan(...any) error }) (model.Addon, error) {
	var a model.Addon
	var auto, service int
	err := row.Scan(&a.ID, &a.Name, &a.Slug, &a.Engine, &a.Kind, &a.OwnerProjectID,
		&a.PinnedServer, &a.Port, &a.DataDir, &a.SizeLimitMB, &a.ReplicationMode,
		&a.Status, &a.LastError, &a.EnvEnc, &a.ConfigEnc, &service, &a.AppID, &a.Version,
		&a.CreatedAt, &a.UpdatedAt, &a.Hostname, &a.BridgePort, &a.Epoch,
		&auto, &a.FailoverAfterSec, &a.MaxLagMS, &a.LeaseSec, &a.LeaseUntil)
	a.AutoFailover = auto != 0
	a.Service = service != 0
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (s *Store) CreateAddon(a model.Addon) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = now()
	}
	if a.UpdatedAt == 0 {
		a.UpdatedAt = a.CreatedAt
	}
	if a.Kind == "" {
		a.Kind = "database"
	}
	if a.ReplicationMode == "" {
		a.ReplicationMode = "none"
	}
	if a.Epoch <= 0 {
		a.Epoch = 1
	}
	auto, service := 0, 0
	if a.AutoFailover {
		auto = 1
	}
	if a.Service {
		service = 1
	}
	_, err := s.db.Exec(`INSERT INTO addons
(id,name,slug,engine,kind,owner_project_id,pinned_server,port,data_dir,size_limit_mb,replication_mode,status,last_error,env_enc,config_enc,service,app_id,version,created_at,updated_at,hostname,bridge_port,epoch,auto_failover,failover_after_sec,max_lag_ms,lease_sec,lease_until)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.Name, a.Slug, a.Engine, a.Kind, a.OwnerProjectID, a.PinnedServer, a.Port,
		a.DataDir, a.SizeLimitMB, a.ReplicationMode, a.Status, a.LastError, a.EnvEnc, a.ConfigEnc,
		service, a.AppID, a.Version, a.CreatedAt, a.UpdatedAt, a.Hostname, a.BridgePort, a.Epoch,
		auto, a.FailoverAfterSec, a.MaxLagMS, a.LeaseSec, a.LeaseUntil)
	return err
}

func (s *Store) UpdateAddon(a model.Addon) error {
	auto := 0
	if a.AutoFailover {
		auto = 1
	}
	_, err := s.db.Exec(`UPDATE addons SET name=?, slug=?, port=?, data_dir=?, size_limit_mb=?,
replication_mode=?, status=?, last_error=?, env_enc=?, pinned_server=?, hostname=?, bridge_port=?, epoch=?,
auto_failover=?, failover_after_sec=?, max_lag_ms=?, lease_sec=?, lease_until=?, updated_at=? WHERE id=?`,
		a.Name, a.Slug, a.Port, a.DataDir, a.SizeLimitMB, a.ReplicationMode, a.Status,
		a.LastError, a.EnvEnc, a.PinnedServer, a.Hostname, a.BridgePort, a.Epoch,
		auto, a.FailoverAfterSec, a.MaxLagMS, a.LeaseSec, a.LeaseUntil, now(), a.ID)
	return err
}

func (s *Store) RenameAddon(id, name string) error {
	_, err := s.db.Exec(`UPDATE addons SET name=?, updated_at=? WHERE id=?`, name, now(), id)
	return err
}

func (s *Store) SetAddonLease(id string, until int64) error {
	_, err := s.db.Exec(`UPDATE addons SET lease_until=?, updated_at=? WHERE id=?`, until, now(), id)
	return err
}

func (s *Store) SetAddonStatus(id, status, lastErr string) error {
	_, err := s.db.Exec(`UPDATE addons SET status=?, last_error=?, updated_at=? WHERE id=?`,
		status, lastErr, now(), id)
	return err
}

// ResetAddonsOffline is called at boot, mirroring ResetServersOffline. Addons
// are not stopped: the phone keeps them running and the heartbeat re-adopts
// them, but the panel must not pretend they are reachable.
func (s *Store) ResetAddonsOffline() error {
	_, err := s.db.Exec(`UPDATE addons SET status='unreachable', updated_at=? WHERE status IN ('running','provisioning','starting','restoring')`, now())
	return err
}

func (s *Store) GetAddon(id string) (model.Addon, error) {
	return s.scanAddon(s.db.QueryRow(`SELECT `+addonCols+` FROM addons WHERE id=?`, id))
}

func (s *Store) GetAddonBySlug(slug string) (model.Addon, error) {
	return s.scanAddon(s.db.QueryRow(`SELECT `+addonCols+` FROM addons WHERE slug=?`, slug))
}

func (s *Store) GetAddonByHostname(host string) (model.Addon, error) {
	return s.scanAddon(s.db.QueryRow(`SELECT `+addonCols+` FROM addons WHERE hostname=? AND hostname<>''`, host))
}

func (s *Store) ListAddons() ([]model.Addon, error) {
	rows, err := s.db.Query(`SELECT ` + addonCols + ` FROM addons ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Addon{}
	for rows.Next() {
		a, err := s.scanAddon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAddon(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM addon_logs WHERE addon_id=?`,
		`DELETE FROM addon_placements WHERE addon_id=?`,
		`DELETE FROM addon_bindings WHERE addon_id=?`,
		`DELETE FROM addon_relays WHERE addon_id=?`,
		`DELETE FROM backups WHERE addon_id=?`,
		`DELETE FROM addons WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- placements ----

func (s *Store) UpsertAddonPlacement(p model.AddonPlacement) error {
	if p.UpdatedAt == 0 {
		p.UpdatedAt = now()
	}
	if p.Role == "" {
		p.Role = "primary"
	}
	_, err := s.db.Exec(`INSERT INTO addon_placements
(addon_id,server_id,role,status,desired,port,pid,restarts,lag_ms,last_error,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(addon_id,server_id) DO UPDATE SET
  role=excluded.role, status=excluded.status, desired=excluded.desired, port=excluded.port,
  pid=excluded.pid, restarts=excluded.restarts, lag_ms=excluded.lag_ms,
  last_error=excluded.last_error, updated_at=excluded.updated_at`,
		p.AddonID, p.ServerID, p.Role, p.Status, desiredOrDefault(p.Desired), p.Port,
		p.PID, p.Restarts, p.LagMS, p.LastError, p.UpdatedAt)
	return err
}

func (s *Store) ListAddonPlacements(addonID string) ([]model.AddonPlacement, error) {
	rows, err := s.db.Query(`SELECT addon_id,server_id,role,status,desired,port,pid,restarts,lag_ms,last_error,updated_at
FROM addon_placements WHERE addon_id=?
ORDER BY CASE role WHEN 'primary' THEN 0 WHEN '' THEN 0 WHEN 'standby' THEN 1 ELSE 2 END, server_id`, addonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AddonPlacement{}
	for rows.Next() {
		var p model.AddonPlacement
		if err := rows.Scan(&p.AddonID, &p.ServerID, &p.Role, &p.Status, &p.Desired, &p.Port,
			&p.PID, &p.Restarts, &p.LagMS, &p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ListAddonPlacementsForServer(serverID string) ([]model.AddonPlacement, error) {
	rows, err := s.db.Query(`SELECT addon_id,server_id,role,status,desired,port,pid,restarts,lag_ms,last_error,updated_at
FROM addon_placements WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AddonPlacement{}
	for rows.Next() {
		var p model.AddonPlacement
		if err := rows.Scan(&p.AddonID, &p.ServerID, &p.Role, &p.Status, &p.Desired, &p.Port,
			&p.PID, &p.Restarts, &p.LagMS, &p.LastError, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) UpdateAddonPlacementStatus(addonID, serverID, status string, pid, restarts int, lastErr string) error {
	_, err := s.db.Exec(`UPDATE addon_placements SET status=?, pid=?, restarts=?, last_error=?, updated_at=?
WHERE addon_id=? AND server_id=?`, status, pid, restarts, lastErr, now(), addonID, serverID)
	return err
}

func (s *Store) SetAddonPlacementLag(addonID, serverID string, lagMS int64) error {
	_, err := s.db.Exec(`UPDATE addon_placements SET lag_ms=?, updated_at=? WHERE addon_id=? AND server_id=?`,
		lagMS, now(), addonID, serverID)
	return err
}

func (s *Store) SetAddonPlacementRole(addonID, serverID, role, desired string) error {
	_, err := s.db.Exec(`UPDATE addon_placements SET role=?, desired=?, updated_at=? WHERE addon_id=? AND server_id=?`,
		role, desired, now(), addonID, serverID)
	return err
}

func (s *Store) SetAddonDesired(addonID, desired string) error {
	_, err := s.db.Exec(`UPDATE addon_placements SET desired=?, updated_at=? WHERE addon_id=?`, desired, now(), addonID)
	return err
}

func (s *Store) DeleteAddonPlacement(addonID, serverID string) error {
	_, err := s.db.Exec(`DELETE FROM addon_placements WHERE addon_id=? AND server_id=?`, addonID, serverID)
	return err
}

// UsedRelayPorts returns the loopback facade ports already allocated on one
// phone. Relays live in a range of their own, separate from app/addon ports.
func (s *Store) UsedRelayPorts(serverID string) (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT port FROM addon_relays WHERE server_id=? AND port>0`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// UsedBridgePorts returns the control-plane loopback ports for addons.
func (s *Store) UsedBridgePorts() (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT bridge_port FROM addons WHERE bridge_port>0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var p int
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out[p] = true
	}
	return out, rows.Err()
}

// ---- relays ----

func (s *Store) UpsertAddonRelay(r model.AddonRelay) error {
	if r.UpdatedAt == 0 {
		r.UpdatedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO addon_relays (addon_id,server_id,port,updated_at) VALUES (?,?,?,?)
ON CONFLICT(addon_id,server_id) DO UPDATE SET port=excluded.port, updated_at=excluded.updated_at`,
		r.AddonID, r.ServerID, r.Port, r.UpdatedAt)
	return err
}

func (s *Store) GetAddonRelay(addonID, serverID string) (model.AddonRelay, error) {
	var r model.AddonRelay
	err := s.db.QueryRow(`SELECT addon_id,server_id,port,updated_at FROM addon_relays WHERE addon_id=? AND server_id=?`,
		addonID, serverID).
		Scan(&r.AddonID, &r.ServerID, &r.Port, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func (s *Store) ListAddonRelays(addonID string) ([]model.AddonRelay, error) {
	rows, err := s.db.Query(`SELECT addon_id,server_id,port,updated_at FROM addon_relays WHERE addon_id=? ORDER BY server_id`, addonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelays(rows)
}

func (s *Store) ListAddonRelaysForServer(serverID string) ([]model.AddonRelay, error) {
	rows, err := s.db.Query(`SELECT addon_id,server_id,port,updated_at FROM addon_relays WHERE server_id=?`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRelays(rows)
}

func scanRelays(rows *sql.Rows) ([]model.AddonRelay, error) {
	out := []model.AddonRelay{}
	for rows.Next() {
		var r model.AddonRelay
		if err := rows.Scan(&r.AddonID, &r.ServerID, &r.Port, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAddonRelay(addonID, serverID string) error {
	_, err := s.db.Exec(`DELETE FROM addon_relays WHERE addon_id=? AND server_id=?`, addonID, serverID)
	return err
}

func (s *Store) DeleteAddonRelays(addonID string) error {
	_, err := s.db.Exec(`DELETE FROM addon_relays WHERE addon_id=?`, addonID)
	return err
}

// ---- bindings ----

func (s *Store) BindAddon(addonID, projectID string) error {
	_, err := s.db.Exec(`INSERT INTO addon_bindings (addon_id,project_id,created_at) VALUES (?,?,?)
ON CONFLICT(addon_id,project_id) DO NOTHING`, addonID, projectID, now())
	return err
}

func (s *Store) UnbindAddon(addonID, projectID string) error {
	_, err := s.db.Exec(`DELETE FROM addon_bindings WHERE addon_id=? AND project_id=?`, addonID, projectID)
	return err
}

func (s *Store) ListAddonBindings(addonID string) ([]model.AddonBinding, error) {
	rows, err := s.db.Query(`SELECT addon_id,project_id,created_at FROM addon_bindings WHERE addon_id=? ORDER BY created_at`, addonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AddonBinding{}
	for rows.Next() {
		var b model.AddonBinding
		if err := rows.Scan(&b.AddonID, &b.ProjectID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) ListProjectAddons(projectID string) ([]model.Addon, error) {
	rows, err := s.db.Query(`SELECT `+addonCols+` FROM addons WHERE id IN
(SELECT addon_id FROM addon_bindings WHERE project_id=?) ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Addon{}
	for rows.Next() {
		a, err := s.scanAddon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteProjectBindings detaches a project from every addon it used.
func (s *Store) DeleteProjectBindings(projectID string) error {
	_, err := s.db.Exec(`DELETE FROM addon_bindings WHERE project_id=?`, projectID)
	return err
}

// OwnedAddons lists addons created inline by a project. Deleting such a
// project is refused while they exist so data is never silently orphaned.
func (s *Store) OwnedAddons(projectID string) ([]model.Addon, error) {
	rows, err := s.db.Query(`SELECT `+addonCols+` FROM addons WHERE owner_project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Addon{}
	for rows.Next() {
		a, err := s.scanAddon(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- logs ----

func (s *Store) AddAddonLog(addonID, line string) error {
	_, err := s.db.Exec(`INSERT INTO addon_logs (addon_id,ts,line) VALUES (?,?,?)`, addonID, now(), line)
	return err
}

func (s *Store) ListAddonLogs(addonID string, limit int) ([]model.DeployLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 300
	}
	rows, err := s.db.Query(`SELECT ts,line FROM addon_logs WHERE addon_id=? ORDER BY id DESC LIMIT ?`, addonID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.DeployLog{}
	for rows.Next() {
		var l model.DeployLog
		if err := rows.Scan(&l.TS, &l.Line); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

func (s *Store) PruneAddonLogs(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM addon_logs WHERE ts < ?`, cutoff)
	return err
}

// ---- backups ----

func (s *Store) CreateBackup(b model.Backup) error {
	if b.CreatedAt == 0 {
		b.CreatedAt = now()
	}
	if b.Status == "" {
		b.Status = "running"
	}
	_, err := s.db.Exec(`INSERT INTO backups (id,addon_id,server_id,kind,status,size,checksum,encrypted,path,error,created_at,finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		b.ID, b.AddonID, b.ServerID, b.Kind, b.Status, b.Size, b.Checksum, b.Encrypted, b.Path, b.Error, b.CreatedAt, b.FinishedAt)
	return err
}

const backupCols = `id,addon_id,server_id,kind,status,size,checksum,encrypted,path,error,created_at,finished_at`

func (s *Store) scanBackup(row interface{ Scan(...any) error }) (model.Backup, error) {
	var b model.Backup
	var encrypted int
	err := row.Scan(&b.ID, &b.AddonID, &b.ServerID, &b.Kind, &b.Status, &b.Size, &b.Checksum, &encrypted, &b.Path, &b.Error, &b.CreatedAt, &b.FinishedAt)
	b.Encrypted = encrypted != 0
	if errors.Is(err, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

func (s *Store) GetBackup(id string) (model.Backup, error) {
	return s.scanBackup(s.db.QueryRow(`SELECT `+backupCols+` FROM backups WHERE id=?`, id))
}

func (s *Store) ListBackups(addonID string, limit int) ([]model.Backup, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+backupCols+` FROM backups WHERE addon_id=? ORDER BY created_at DESC LIMIT ?`, addonID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Backup{}
	for rows.Next() {
		b, err := s.scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) SetBackupChecksum(id, checksum string) error {
	_, err := s.db.Exec(`UPDATE backups SET checksum=? WHERE id=?`, checksum, id)
	return err
}

func (s *Store) SetBackupEncrypted(id string) error {
	_, err := s.db.Exec(`UPDATE backups SET encrypted=1 WHERE id=?`, id)
	return err
}

func (s *Store) FinishBackup(id, status string, size int64, path, errMsg string) error {
	_, err := s.db.Exec(`UPDATE backups SET status=?, size=?, path=?, error=?, finished_at=? WHERE id=?`,
		status, size, path, errMsg, now(), id)
	return err
}

func (s *Store) DeleteBackup(id string) error {
	_, err := s.db.Exec(`DELETE FROM backups WHERE id=?`, id)
	return err
}

func (s *Store) PruneBackups(addonID string, keep int) ([]model.Backup, error) {
	if keep <= 0 {
		keep = 10
	}
	rows, err := s.db.Query(`SELECT `+backupCols+` FROM backups WHERE addon_id=? ORDER BY created_at DESC`, addonID)
	if err != nil {
		return nil, err
	}
	var all []model.Backup
	for rows.Next() {
		b, err := s.scanBackup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, b)
	}
	rows.Close()
	if len(all) <= keep {
		return nil, nil
	}
	removed := []model.Backup{}
	for _, b := range all[keep:] {
		if err := s.DeleteBackup(b.ID); err == nil {
			removed = append(removed, b)
		}
	}
	return removed, nil
}
