package store

import (
	"database/sql"
	"errors"
	"math"

	"phleet/internal/model"
)

func (s *Store) InsertAppMetric(projectID, serverID string, ts int64, cpu float64, rss int64) error {
	if ts == 0 {
		ts = now()
	}
	_, err := s.db.Exec(`INSERT INTO app_metrics (project_id,server_id,ts,cpu,rss) VALUES (?,?,?,?,?)`,
		projectID, serverID, ts, cpu, rss)
	return err
}

// QueryAppMetrics returns per-project samples newer than since, downsampled to
// bucketSec.
func (s *Store) QueryAppMetrics(projectID string, since, bucketSec int64) ([]model.AppSample, error) {
	if bucketSec < 1 {
		bucketSec = 1
	}
	rows, err := s.db.Query(`
SELECT (ts/?)*? AS b, AVG(cpu), AVG(rss)
FROM app_metrics WHERE project_id=? AND ts>=? GROUP BY b ORDER BY b`,
		bucketSec, bucketSec, projectID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AppSample{}
	for rows.Next() {
		var x model.AppSample
		var rss float64
		if err := rows.Scan(&x.TS, &x.CPUPercent, &rss); err != nil {
			return nil, err
		}
		x.RSS = int64(math.Round(rss))
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) PruneAppMetrics(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM app_metrics WHERE ts < ?`, cutoff)
	return err
}

func (s *Store) InsertRequestLog(l model.RequestLog) error {
	if l.TS == 0 {
		l.TS = now()
	}
	_, err := s.db.Exec(`INSERT INTO request_logs
(project_id,server_id,ts,method,path,status,bytes,duration_ms,host,ip)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		l.ProjectID, l.ServerID, l.TS, l.Method, l.Path, l.Status, l.Bytes, l.DurationMS, l.Host, l.IP)
	return err
}

func (s *Store) ListRequestLogs(projectID string, limit int, since int64) ([]model.RequestLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id,project_id,server_id,ts,method,path,status,bytes,duration_ms,host,ip
FROM request_logs WHERE project_id=? AND ts>=? ORDER BY ts DESC, id DESC LIMIT ?`, projectID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.RequestLog{}
	for rows.Next() {
		var l model.RequestLog
		if err := rows.Scan(&l.ID, &l.ProjectID, &l.ServerID, &l.TS, &l.Method, &l.Path,
			&l.Status, &l.Bytes, &l.DurationMS, &l.Host, &l.IP); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) PruneRequestLogs(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM request_logs WHERE ts < ?`, cutoff)
	return err
}

func (s *Store) SetUptimeStatus(u model.UptimeStatus) error {
	_, err := s.db.Exec(`INSERT INTO uptime_checks (project_id,checked_at,ok,status_code,error)
VALUES (?,?,?,?,?)
ON CONFLICT(project_id) DO UPDATE SET checked_at=excluded.checked_at, ok=excluded.ok,
  status_code=excluded.status_code, error=excluded.error`,
		u.ProjectID, u.CheckedAt, boolInt(u.OK), u.StatusCode, u.Error)
	return err
}

func (s *Store) GetUptimeStatus(projectID string) (model.UptimeStatus, error) {
	var u model.UptimeStatus
	var ok int
	err := s.db.QueryRow(`SELECT project_id,checked_at,ok,status_code,error FROM uptime_checks WHERE project_id=?`, projectID).
		Scan(&u.ProjectID, &u.CheckedAt, &ok, &u.StatusCode, &u.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	u.OK = ok != 0
	return u, err
}

func (s *Store) ListUptimeStatuses() ([]model.UptimeStatus, error) {
	rows, err := s.db.Query(`SELECT project_id,checked_at,ok,status_code,error FROM uptime_checks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.UptimeStatus{}
	for rows.Next() {
		var u model.UptimeStatus
		var ok int
		if err := rows.Scan(&u.ProjectID, &u.CheckedAt, &ok, &u.StatusCode, &u.Error); err != nil {
			return nil, err
		}
		u.OK = ok != 0
		out = append(out, u)
	}
	return out, rows.Err()
}
