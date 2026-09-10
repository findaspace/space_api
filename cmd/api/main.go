package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/platform/postgres"
	"github.com/samnart1/space_api/internal/platform/telemetry"
	"github.com/samnart1/space_api/internal/transport/httpapi"
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
		slog.String("service", "space_api"),
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

	router := httpapi.NewRouter(httpapi.Deps{
		Pool:         pool,
		Logger:       log,
		PingTimeout:  cfg.DBPingTimeout,
		MaxBodyBytes: 1 << 20,
		Version:      version,
		Commit:       commit,
	})

	return httpapi.Run(ctx, httpapi.ServerConfig{
		Addr:            cfg.HTTPAddr,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		IdleTimeout:     cfg.IdleTimeout,
		ShutdownTimeout: cfg.ShudownTimeout,
	}, router, log)
}
