// Package handler — test helpers for tests in this package.
// This file lives in package handler so other _test.go files can
// call openTestDB / nowMs / sqliteConn directly.

package handler

import (
	"database/sql"
	"path/filepath"
	"testing"

	"intelligent_customer/backend/internal/testutil"
)

// sqliteConn is a thin alias for *sql.DB so test fixtures can name the
// return type cleanly. The real type comes from the standard library.
type sqliteConn = sql.DB

// openTestDB returns a fresh in-memory SQLite connection plus a cleanup
// func. We delegate to testutil.OpenTempSQLite (which already supports
// both on-disk and :memory: modes) and re-type the result.
//
// The reason we have this wrapper: the existing handler test fixtures
// reference (*sql.DB) implicitly; this helper gives them an alias they
// can rely on across the package.
func openTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	conn, _ := testutil.OpenTempSQLite(t)
	return conn, func() {
		// testutil.OpenTempSQLite already registers a t.Cleanup that
		// closes the connection; the returned cleanup is a no-op.
		_ = filepath.Separator // keep imports stable
	}
}