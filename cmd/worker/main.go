package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/jobs"
	"github.com/samnart1/space_api/internal/platform/postgres"
	"github.com/samnart1/space_api/internal/platform/telemetry"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(cfg.LogLevel, cfg.LogFormat).With(
		slog.String("service", "space_worker"),
		slog.String("version", version),
		slog.String("commit", commit),
	)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:          cfg.DatabaseUrl,
		MaxConns:     cfg.DBMaxConns,
		MinConns:     cfg.DBMinConns,
		ConnLifetime: cfg.DBConnLifetime,
		PingTimeout:  cfg.DBPingTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	deps, err := buildDeps(ctx, cfg, pool, log)
	if err != nil {
		return err
	}

	workers, err := jobs.Register(deps)
	if err != nil {
		return err
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:          map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
		Workers:         workers,
		PeriodicJobs:    jobs.Periodic(),
		SoftStopTimeout: 30 * time.Second,
		Logger:          log,
	})
	if err != nil {
		return fmt.Errorf("river client: %w", err)
	}

	log.Info("worker starting", slog.Int("max_workers", 10))

	if err := client.Start(ctx); err != nil {
		return fmt.Errorf("start river: %w", err)
	}

	<-client.Stopped()
	log.Info("worker stopped")
	return nil
}

var _ = pgx.Tx(nil)
