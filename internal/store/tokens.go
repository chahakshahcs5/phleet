package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

func (s *Store) CreateAPIToken(t model.APIToken, hash string) error {
	if t.CreatedAt == 0 {
		t.CreatedAt = now()
	}
	if t.Role == "" {
		t.Role = "viewer"
	}
	_, err := s.db.Exec(`INSERT INTO api_tokens (id,name,role,token_hash,created_at,last_used_at)
VALUES (?,?,?,?,?,0)`, t.ID, t.Name, t.Role, hash, t.CreatedAt)
	return err
}

func (s *Store) ListAPITokens() ([]model.APIToken, error) {
	rows, err := s.db.Query(`SELECT id,name,role,created_at,last_used_at FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.APIToken{}
	for rows.Next() {
		var t model.APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Role, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetAPITokenByHash(hash string) (model.APIToken, error) {
	var t model.APIToken
	err := s.db.QueryRow(`SELECT id,name,role,created_at,last_used_at FROM api_tokens WHERE token_hash=?`, hash).
		Scan(&t.ID, &t.Name, &t.Role, &t.CreatedAt, &t.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) TouchAPIToken(id string) error {
	_, err := s.db.Exec(`UPDATE api_tokens SET last_used_at=? WHERE id=?`, now(), id)
	return err
}

func (s *Store) DeleteAPIToken(id string) error {
	_, err := s.db.Exec(`DELETE FROM api_tokens WHERE id=?`, id)
	return err
}
