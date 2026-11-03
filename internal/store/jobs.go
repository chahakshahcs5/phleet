package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

func (s *Store) CreateJob(j model.Job) error {
	if j.CreatedAt == 0 {
		j.CreatedAt = now()
	}
	if j.Status == "" {
		j.Status = "running"
	}
	_, err := s.db.Exec(`INSERT INTO jobs (id,kind,target,status,output,error,created_at,updated_at)
VALUES (?,?,?,?,?,?,?,?)`,
		j.ID, j.Kind, j.Target, j.Status, j.Output, j.Error, j.CreatedAt, j.CreatedAt)
	return err
}

func (s *Store) AppendJobOutput(id, chunk string) error {
	// Keep the row bounded; onboarding output can be verbose and ANSI-heavy.
	_, err := s.db.Exec(`UPDATE jobs SET output=substr(output || ?, -200000), updated_at=? WHERE id=?`,
		chunk, now(), id)
	return err
}

func (s *Store) FinishJob(id, status, errMsg string) error {
	_, err := s.db.Exec(`UPDATE jobs SET status=?, error=?, updated_at=? WHERE id=?`, status, errMsg, now(), id)
	return err
}

func (s *Store) GetJob(id string) (model.Job, error) {
	var j model.Job
	err := s.db.QueryRow(`SELECT id,kind,target,status,output,error,created_at,updated_at FROM jobs WHERE id=?`, id).
		Scan(&j.ID, &j.Kind, &j.Target, &j.Status, &j.Output, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

func (s *Store) PruneJobs(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM jobs WHERE created_at < ?`, cutoff)
	return err
}
