package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"intelligent_customer/backend/internal/model"
)

// AdminUsers owns the admin_users table. All operations here are plain SQL;
// password hashing lives in internal/security so the repo stays dumb and
// testable.
type AdminUsers struct {
	DB *sql.DB
}

func NewAdminUsers(db *sql.DB) *AdminUsers { return &AdminUsers{DB: db} }

// ErrNotFound is returned when a username lookup misses. The CLI uses this
// to differentiate "wrong username" from "DB problem".
var ErrNotFound = errors.New("admin user not found")

// GetByUsername returns the admin row for the exact username (case-sensitive,
// matching the UNIQUE index).
func (r *AdminUsers) GetByUsername(ctx context.Context, username string) (*model.AdminUser, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, updated_at, last_login_at
		 FROM admin_users WHERE username = ?`, username)
	return scanAdmin(row)
}

// GetByID is used by the CLI when the operator passes an id rather than a
// username (rename / delete / passwd accept either).
func (r *AdminUsers) GetByID(ctx context.Context, id string) (*model.AdminUser, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, created_at, updated_at, last_login_at
		 FROM admin_users WHERE id = ?`, id)
	return scanAdmin(row)
}

// Insert creates a new admin row. Username must be unique; callers should
// pre-check with GetByUsername if they want a friendly 409 instead of a
// raw UNIQUE-constraint error.
func (r *AdminUsers) Insert(ctx context.Context, username, passwordHash, role string) (*model.AdminUser, error) {
	now := time.Now().UnixMilli()
	u := &model.AdminUser{
		ID:           uuid.NewString(),
		Username:     username,
		PasswordHash: passwordHash,
		Role:         role,
		CreatedAt:    time.UnixMilli(now),
		UpdatedAt:    time.UnixMilli(now),
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO admin_users(id, username, password_hash, role, created_at, updated_at)
		 VALUES(?,?,?,?,?,?)`,
		u.ID, u.Username, u.PasswordHash, u.Role, now, now)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateUsername renames the user identified by id and bumps updated_at.
// Returns ErrNotFound when no row matches.
func (r *AdminUsers) UpdateUsername(ctx context.Context, id, newUsername string) error {
	now := time.Now().UnixMilli()
	res, err := r.DB.ExecContext(ctx,
		`UPDATE admin_users SET username=?, updated_at=? WHERE id=?`,
		newUsername, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePassword replaces the Argon2id-encoded password and bumps updated_at.
func (r *AdminUsers) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	now := time.Now().UnixMilli()
	res, err := r.DB.ExecContext(ctx,
		`UPDATE admin_users SET password_hash=?, updated_at=? WHERE id=?`,
		passwordHash, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchLastLogin records a successful login. nil ts means "now".
func (r *AdminUsers) TouchLastLogin(ctx context.Context, id string) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE admin_users SET last_login_at=? WHERE id=?`,
		time.Now().UnixMilli(), id)
	return err
}

// Delete removes the row and returns ErrNotFound when id didn't match.
func (r *AdminUsers) Delete(ctx context.Context, id string) error {
	res, err := r.DB.ExecContext(ctx, `DELETE FROM admin_users WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns every admin row ordered by username. Bounded — we expect
// at most a handful of accounts.
func (r *AdminUsers) List(ctx context.Context) ([]model.AdminUser, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, username, password_hash, role, created_at, updated_at, last_login_at
		 FROM admin_users ORDER BY username ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.AdminUser, 0, 4)
	for rows.Next() {
		u, err := scanAdmin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// Count returns the row count; used by the CLI to guard "delete the last
// admin" mistakes.
func (r *AdminUsers) Count(ctx context.Context) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&n)
	return n, err
}

// LookupByUsernameOrID accepts either form, so the CLI can be flexible:
//   adminctl passwd admin
//   adminctl passwd 5d3c...uuid...
func (r *AdminUsers) LookupByUsernameOrID(ctx context.Context, key string) (*model.AdminUser, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrNotFound
	}
	if u, err := r.GetByUsername(ctx, key); err == nil {
		return u, nil
	}
	return r.GetByID(ctx, key)
}

// scanAdmin reads one row regardless of whether it came from QueryRow or
// QueryContext (the only common surface is Scan).
func scanAdmin(s interface {
	Scan(dest ...any) error
}) (*model.AdminUser, error) {
	var (
		u         model.AdminUser
		lastLogin sql.NullInt64
		createdAt int64
		updatedAt int64
	)
	err := s.Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role,
		&createdAt, &updatedAt, &lastLogin,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.CreatedAt = time.UnixMilli(createdAt)
	u.UpdatedAt = time.UnixMilli(updatedAt)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		u.LastLoginAt = &t
	}
	return &u, nil
}