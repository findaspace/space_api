package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/samnart1/space_api/db"
	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:          cfg.DatabaseUrl,
		MaxConns:     2,
		MinConns:     1,
		ConnLifetime: 5 * time.Minute,
		PingTimeout:  cfg.DBPingTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	fsys, err := db.Migrations()
	if err != nil {
		return err
	}

	version, err := postgres.Migrate(ctx, pool, fsys)
	if err != nil {
		return err
	}

	fmt.Printf("schema at version %d\n", version)
	return nil
}
