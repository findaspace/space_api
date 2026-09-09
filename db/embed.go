package db

import (
	"embed"
	"io/fs"
)

var migrationsFS embed.FS

func Migrations() (fs.FS, error) {
	return fs.Sub(migrationsFS, "migrations")
}
