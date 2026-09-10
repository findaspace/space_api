package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations returns the migration files rooted at the directory itself, which
// is what goose expects. Embedding ships the schema inside the binary, so a
// deploy can never apply migrations from a different build.
func Migrations() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}
