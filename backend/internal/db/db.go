// Package db handles SQLite open + migrations. We use modernc.org/sqlite so
// the resulting binary is fully static (no CGO toolchain dependency).
package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// modernc.org/sqlite registers as "sqlite".
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single writer at a time keeps things simple; SQLite handles concurrency
	// for reads on its own.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(time.Hour)

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return conn, nil
}

// Migrate runs all bundled SQL migrations in lexical order. It is idempotent:
// a schema_migrations table tracks applied filenames, and the bootstrap path
// recognises legacy databases (schema present, no migration history) so a
// v2.1.x → v2.2 upgrade doesn't crash on a duplicate-column error.
func Migrate(conn *sql.DB, fsys fs.FS, sub string) error {
	// Tracking table must exist before we can query it.
	if _, err := conn.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			name TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(fsys, sub)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", sub, err)
	}

	// Bootstrap path: legacy DB that already has our tables but no
	// schema_migrations history. Mark every current .sql as already applied
	// so we never try to re-apply a non-idempotent ALTER ADD COLUMN.
	var tracked int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&tracked); err != nil {
		return fmt.Errorf("count schema_migrations: %w", err)
	}
	if tracked == 0 {
		var legacyTables int
		if err := conn.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('admin_users','tenants','roles')`,
		).Scan(&legacyTables); err != nil {
			return fmt.Errorf("legacy detect: %w", err)
		}
		if legacyTables > 0 {
			now := time.Now().Unix()
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if _, err := conn.Exec(
					`INSERT OR IGNORE INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
					e.Name(), now,
				); err != nil {
					return fmt.Errorf("bootstrap insert %s: %w", e.Name(), err)
				}
			}
			return nil
		}
	}

	// Normal path: apply only migrations that haven't been recorded yet.
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var applied int
		if err := conn.QueryRow(
			`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}

		path := name
		if sub != "" {
			path = sub + "/" + name
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if _, err := conn.Exec(string(b)); err != nil {
			return fmt.Errorf("apply %s: %w", path, err)
		}
		if _, err := conn.Exec(
			`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().Unix(),
		); err != nil {
			return fmt.Errorf("mark %s: %w", name, err)
		}
	}
	return nil
}