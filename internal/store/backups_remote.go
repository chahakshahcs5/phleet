package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

func (s *Store) SetBackupRemote(id, status, key, errMsg string) error {
	_, err := s.db.Exec(`UPDATE backups SET remote_status=?, remote_key=?, remote_error=?, remote_at=? WHERE id=?`,
		status, key, errMsg, now(), id)
	return err
}

func (s *Store) SetBackupPath(id, path string) error {
	_, err := s.db.Exec(`UPDATE backups SET path=? WHERE id=?`, path, id)
	return err
}

func (s *Store) CreateDrill(d model.RestoreDrill) error {
	if d.StartedAt == 0 {
		d.StartedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO backup_drills (id,addon_id,backup_id,status,detail,started_at,finished_at)
VALUES (?,?,?,?,?,?,?)`,
		d.ID, d.AddonID, d.BackupID, d.Status, d.Detail, d.StartedAt, d.FinishedAt)
	return err
}

func (s *Store) FinishDrill(id, status, detail string) error {
	_, err := s.db.Exec(`UPDATE backup_drills SET status=?, detail=?, finished_at=? WHERE id=?`,
		status, detail, now(), id)
	return err
}

const drillCols = `id,addon_id,backup_id,status,detail,started_at,finished_at`

func (s *Store) scanDrill(row interface{ Scan(...any) error }) (model.RestoreDrill, error) {
	var d model.RestoreDrill
	err := row.Scan(&d.ID, &d.AddonID, &d.BackupID, &d.Status, &d.Detail, &d.StartedAt, &d.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

func (s *Store) ListDrills(addonID string, limit int) ([]model.RestoreDrill, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT `+drillCols+` FROM backup_drills WHERE addon_id=? ORDER BY started_at DESC, id DESC LIMIT ?`, addonID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.RestoreDrill{}
	for rows.Next() {
		d, err := s.scanDrill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) LatestDrill(addonID string) (model.RestoreDrill, error) {
	return s.scanDrill(s.db.QueryRow(`SELECT `+drillCols+` FROM backup_drills WHERE addon_id=? ORDER BY started_at DESC, id DESC LIMIT 1`, addonID))
}

func (s *Store) PruneDrills(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM backup_drills WHERE started_at < ?`, cutoff)
	return err
}
