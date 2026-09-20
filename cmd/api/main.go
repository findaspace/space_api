package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/platform/postgres"
	"github.com/samnart1/space_api/internal/platform/sms"
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

	hasher, err := identity.NewHasher(cfg.OTPPepper)
	if err != nil {
		return err
	}

	tokens, err := identity.NewTokenIssuer(cfg.JWTSeed, cfg.JWTKeyID, cfg.AccessTTL)
	if err != nil {
		return err
	}

	var sender sms.Sender
	switch cfg.SMSProvider {
	case "log":
		sender = sms.LogSender{Log: log}
	default:
		return fmt.Errorf("unknown SPACE_SMS_PROVIDER %q", cfg.SMSProvider)
	}

	identitySvc := identity.NewService(
		identity.NewStore(pool), hasher, tokens, sender, log,
		identity.ServiceConfig{
			OTPTTL:         cfg.OTPTTL,
			OTPMaxAttempts: int16(cfg.OTPMaxAttempts),
			OTPWindow:      cfg.OTPWindow,
			OTPMaxPerPhone: cfg.OTPMaxPerPhone,
			OTPMaxPerIP:    cfg.OTPMaxPerIp,
			RefreshTTL:     cfg.RefreshTTL,
			SessionMaxAge:  cfg.SessionMaxAge,
		})
	listingSvc := listing.NewService(listing.NewStore(pool), identitySvc)

	router := httpapi.NewRouter(httpapi.Deps{
		Pool:         pool,
		Logger:       log,
		PingTimeout:  cfg.DBPingTimeout,
		MaxBodyBytes: 1 << 20,
		Version:      version,
		Commit:       commit,
		Identity:     identitySvc,
		Tokens:       tokens,
		TrustProxy:   cfg.TrustProxy,
		Listing:      listingSvc,
	})

	return httpapi.Run(ctx, httpapi.ServerConfig{
		Addr:            cfg.HTTPAddr,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		IdleTimeout:     cfg.IdleTimeout,
		ShutdownTimeout: cfg.ShudownTimeout,
	}, router, log)
}
