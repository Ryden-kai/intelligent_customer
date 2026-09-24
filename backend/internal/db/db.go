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

// Migrate runs all bundled SQL migrations in lexical order. It is idempotent.
func Migrate(conn *sql.DB, fsys fs.FS, sub string) error {
	entries, err := fs.ReadDir(fsys, sub)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", sub, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
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
	}
	return nil
}