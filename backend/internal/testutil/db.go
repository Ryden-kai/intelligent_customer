// Package testutil provides shared helpers for integration tests: a
// throwaway SQLite database with migrations applied, plus a tiny logger.
package testutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"intelligent_customer/backend/internal/db"
)

// OpenTempSQLite opens a fresh SQLite file inside t.TempDir(), applies
// migrations and returns the *sql.DB plus a cleanup func. Safe to call from
// any test that wants to exercise the real repo layer.
func OpenTempSQLite(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Migrate(conn, embeddedMigrations, "migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		// On Windows, WAL/SHM sidecar files linger briefly; removing the
		// whole dir is fine once the connection is closed.
		_ = os.RemoveAll(dir)
	})
	return conn, func() {}
}

// QuietLogger returns a no-op logger suitable for tests that don't want to
// pollute their test output.
func QuietLogger() zerolog.Logger { return zerolog.Nop() }
