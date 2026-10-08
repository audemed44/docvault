package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrConflict is a unique value that's already taken (a username).
var ErrConflict = errors.New("already exists")

// ErrInUse is a record that other records still need (a user who owns
// documents).
var ErrInUse = errors.New("still in use")

type User struct {
	ID       int64     `json:"id"`
	Username string    `json:"username"`
	Name     string    `json:"name"`
	Admin    bool      `json:"admin"`
	Created  time.Time `json:"created"`
	// Documents counts the user's private documents (in Users only).
	Documents int `json:"documents"`
}

const userCols = `id, username, name, admin, created`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.Name, &u.Admin, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Created = fromMS(created)
	return &u, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+`,
		(SELECT count(*) FROM documents d WHERE d.owner_id = users.id)
		FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		var u User
		var created int64
		if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Admin, &created, &u.Documents); err != nil {
			return nil, err
		}
		u.Created = fromMS(created)
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) User(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByName(ctx context.Context, username string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE username = ?`, username))
}

func (s *Store) CreateUser(ctx context.Context, u *User) error {
	u.Created = time.Now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, name, admin, created) VALUES (?, ?, ?, ?)`,
		u.Username, u.Name, u.Admin, ms(u.Created))
	if isUnique(err) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

// UpdateUser changes the name and admin flag.
func (s *Store) UpdateUser(ctx context.Context, u *User) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET name = ?, admin = ? WHERE id = ?`, u.Name, u.Admin, u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes a user who has no private documents left.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	var docs int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM documents WHERE owner_id = ?`, id).Scan(&docs); err != nil {
		return err
	}
	if docs > 0 {
		return ErrInUse
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Sessions are stored as hashes of the secret.

func (s *Store) CreateSession(ctx context.Context, hash string, userID int64) error {
	now := ms(time.Now())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (hash, user_id, created, seen) VALUES (?, ?, ?, ?)`, hash, userID, now, now)
	return err
}

// SessionUser finds the user signed in with a session, and notes the visit
// (at most hourly). Sessions unused for maxIdle have expired.
func (s *Store) SessionUser(ctx context.Context, hash string, maxIdle time.Duration) (*User, error) {
	var seen int64
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT u.id, u.username, u.name, u.admin, u.created
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.hash = ?`, hash))
	if err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT seen FROM sessions WHERE hash = ?`, hash).Scan(&seen); err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Sub(fromMS(seen)) > maxIdle {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash)
		return nil, ErrNotFound
	}
	if now.Sub(fromMS(seen)) > time.Hour {
		_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET seen = ? WHERE hash = ?`, ms(now), hash)
	}
	return u, nil
}

func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE hash = ?`, hash)
	return err
}
