package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (int64, error) {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(1864357201), lock.WithLockTimeout(1, 60))
	if err != nil {
		return 0, fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return 0, fmt.Errorf("goose provider: %w", err)
	}

	if _, err := provider.Up(ctx); err != nil {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}
