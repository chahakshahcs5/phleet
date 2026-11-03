package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

// UpsertAlert opens or refreshes an alert. It returns true when the alert
// transitioned from resolved/absent to firing, so callers can notify once.
func (s *Store) UpsertAlert(key, kind, severity, target, message string) (bool, error) {
	var id int64
	var status string
	err := s.db.QueryRow(`SELECT id,status FROM alerts WHERE key=?`, key).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.Exec(`INSERT INTO alerts (key,kind,severity,status,target,message,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?)`, key, kind, severity, "firing", target, message, now(), now())
		return err == nil, err
	}
	if err != nil {
		return false, err
	}
	if status == "resolved" {
		_, err = s.db.Exec(`UPDATE alerts SET kind=?, severity=?, status='firing', target=?, message=?,
created_at=?, updated_at=?, resolved_at=0 WHERE id=?`,
			kind, severity, target, message, now(), now(), id)
		return err == nil, err
	}
	_, err = s.db.Exec(`UPDATE alerts SET severity=?, target=?, message=?, updated_at=? WHERE id=?`,
		severity, target, message, now(), id)
	return false, err
}

func (s *Store) ResolveAlert(key string) (bool, error) {
	res, err := s.db.Exec(`UPDATE alerts SET status='resolved', resolved_at=?, updated_at=? WHERE key=? AND status!='resolved'`,
		now(), now(), key)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) AckAlert(id int64) error {
	_, err := s.db.Exec(`UPDATE alerts SET status='ack', updated_at=? WHERE id=? AND status='firing'`, now(), id)
	return err
}

func (s *Store) ListAlerts(activeOnly bool, limit int) ([]model.Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id,key,kind,severity,status,target,message,created_at,updated_at,resolved_at FROM alerts`
	if activeOnly {
		q += ` WHERE status IN ('firing','ack')`
	}
	q += ` ORDER BY updated_at DESC LIMIT ?`
	rows, err := s.db.Query(q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Alert{}
	for rows.Next() {
		var a model.Alert
		if err := rows.Scan(&a.ID, &a.Key, &a.Kind, &a.Severity, &a.Status, &a.Target, &a.Message,
			&a.CreatedAt, &a.UpdatedAt, &a.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) CountActiveAlerts() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE status IN ('firing','ack')`).Scan(&n)
	return n, err
}

func (s *Store) GetAlertByKey(key string) (model.Alert, error) {
	var a model.Alert
	err := s.db.QueryRow(`SELECT id,key,kind,severity,status,target,message,created_at,updated_at,resolved_at
FROM alerts WHERE key=?`, key).
		Scan(&a.ID, &a.Key, &a.Kind, &a.Severity, &a.Status, &a.Target, &a.Message, &a.CreatedAt, &a.UpdatedAt, &a.ResolvedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// PruneAlerts keeps the alert log bounded.
func (s *Store) PruneAlerts(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM alerts WHERE status='resolved' AND resolved_at < ?`, cutoff)
	return err
}
