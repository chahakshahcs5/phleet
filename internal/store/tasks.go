package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Store) CreateTask(t model.Task) error {
	if t.CreatedAt == 0 {
		t.CreatedAt = now()
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = t.CreatedAt
	}
	_, err := s.db.Exec(`INSERT INTO scheduled_tasks
(id,name,server_id,project_id,command,cron,timeout_sec,enabled,last_run,last_status,last_exit,last_error,last_output,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Name, t.ServerID, t.ProjectID, t.Command, t.Cron, t.TimeoutSec, boolInt(t.Enabled),
		t.LastRun, t.LastStatus, t.LastExit, t.LastError, t.LastOutput, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *Store) UpdateTask(t model.Task) error {
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET name=?, server_id=?, project_id=?, command=?, cron=?,
timeout_sec=?, enabled=?, updated_at=? WHERE id=?`,
		t.Name, t.ServerID, t.ProjectID, t.Command, t.Cron, t.TimeoutSec, boolInt(t.Enabled), now(), t.ID)
	return err
}

func (s *Store) scanTask(row interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var enabled int
	err := row.Scan(&t.ID, &t.Name, &t.ServerID, &t.ProjectID, &t.Command, &t.Cron,
		&t.TimeoutSec, &enabled, &t.LastRun, &t.LastStatus, &t.LastExit, &t.LastError,
		&t.LastOutput, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.Enabled = enabled != 0
	return t, err
}

const taskCols = `id,name,server_id,project_id,command,cron,timeout_sec,enabled,last_run,last_status,last_exit,last_error,last_output,created_at,updated_at`

func (s *Store) GetTask(id string) (model.Task, error) {
	return s.scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM scheduled_tasks WHERE id=?`, id))
}

func (s *Store) ListTasks() ([]model.Task, error) {
	return s.listTasks(`SELECT ` + taskCols + ` FROM scheduled_tasks ORDER BY server_id, name`)
}

func (s *Store) ListTasksForServer(serverID string) ([]model.Task, error) {
	return s.listTasks(`SELECT `+taskCols+` FROM scheduled_tasks WHERE server_id=? ORDER BY name`, serverID)
}

func (s *Store) ListTasksForProject(projectID string) ([]model.Task, error) {
	return s.listTasks(`SELECT `+taskCols+` FROM scheduled_tasks WHERE project_id=? ORDER BY name`, projectID)
}

func (s *Store) listTasks(query string, args ...any) ([]model.Task, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Task{}
	for rows.Next() {
		t, err := s.scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) DeleteTask(id string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_tasks WHERE id=?`, id)
	return err
}

func (s *Store) DeleteTasksForServer(serverID string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_tasks WHERE server_id=?`, serverID)
	return err
}

func (s *Store) DeleteTasksForProject(projectID string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_tasks WHERE project_id=?`, projectID)
	return err
}

// TouchTaskRun records a run reported by the agent. Output is only replaced
// when the caller sends some, so heartbeat status updates do not erase the last
// output captured from a task event.
func (s *Store) TouchTaskRun(id string, lastRun int64, status string, exit int, errMsg, output string) error {
	_, err := s.db.Exec(`UPDATE scheduled_tasks SET last_run=?, last_status=?, last_exit=?, last_error=?,
last_output=CASE WHEN ?='' THEN last_output ELSE ? END, updated_at=? WHERE id=?`,
		lastRun, status, exit, errMsg, output, output, now(), id)
	return err
}
