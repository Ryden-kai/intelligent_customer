package db

import (
	"embed"
	"io/fs"
)

// FS is the embedded migrations filesystem. The cmd/server entrypoint wires
// in the actual embed.FS at startup; here we expose only the FS interface so
// internal packages don't depend on embed.
type FS interface {
	fs.ReadDirFS
	fs.ReadFileFS
}

// MigrationsFS exposes the embedded SQL files. Set by main at build time.
var MigrationsFS FS

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

func init() {
	MigrationsFS = embeddedMigrations
}