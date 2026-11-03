package store

import (
	"database/sql"
	"errors"

	"phleet/internal/model"
)

const userCols = `id,username,password_hash,role,totp_secret,totp_pending,disabled,created_at,updated_at`

func (s *Store) scanUser(row interface{ Scan(...any) error }) (model.User, error) {
	var u model.User
	var disabled int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.TOTPSecret, &u.TOTPPending,
		&disabled, &u.CreatedAt, &u.UpdatedAt)
	u.Disabled = disabled != 0
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func (s *Store) CreateUser(u model.User) error {
	ts := now()
	if u.CreatedAt == 0 {
		u.CreatedAt = ts
	}
	if u.Role == "" {
		u.Role = "viewer"
	}
	_, err := s.db.Exec(`INSERT INTO users
(id,username,password_hash,role,totp_secret,totp_pending,disabled,created_at,updated_at)
VALUES (?,?,?,?,?,?,0,?,?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, u.TOTPSecret, u.TOTPPending, u.CreatedAt, ts)
	return err
}

func (s *Store) GetUser(id string) (model.User, error) {
	return s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (s *Store) GetUserByUsername(username string) (model.User, error) {
	return s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE lower(username)=lower(?)`, username))
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.User{}
	for rows.Next() {
		u, err := s.scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) UpdateUser(u model.User) error {
	disabled := 0
	if u.Disabled {
		disabled = 1
	}
	_, err := s.db.Exec(`UPDATE users SET username=?, password_hash=?, role=?, totp_secret=?,
totp_pending=?, disabled=?, updated_at=? WHERE id=?`,
		u.Username, u.PasswordHash, u.Role, u.TOTPSecret, u.TOTPPending, disabled, now(), u.ID)
	return err
}

func (s *Store) DeleteUser(id string) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}
