package store

type ProjectSecret struct {
	ProjectID  string
	Name       string
	ContentEnc string
	UpdatedAt  int64
}

func (s *Store) SetProjectSecret(projectID, name, contentEnc string) error {
	_, err := s.db.Exec(`INSERT INTO project_secrets (project_id,name,content_enc,updated_at)
VALUES (?,?,?,?)
ON CONFLICT(project_id,name) DO UPDATE SET content_enc=excluded.content_enc, updated_at=excluded.updated_at`,
		projectID, name, contentEnc, now())
	return err
}

func (s *Store) ListProjectSecrets(projectID string) ([]ProjectSecret, error) {
	rows, err := s.db.Query(`SELECT project_id,name,content_enc,updated_at FROM project_secrets WHERE project_id=? ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectSecret{}
	for rows.Next() {
		var x ProjectSecret
		if err := rows.Scan(&x.ProjectID, &x.Name, &x.ContentEnc, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) DeleteProjectSecret(projectID, name string) error {
	_, err := s.db.Exec(`DELETE FROM project_secrets WHERE project_id=? AND name=?`, projectID, name)
	return err
}
