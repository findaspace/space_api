package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL               string
	MaxConns          int32
	MinConns          int32
	ConnLifetime      time.Duration
	PingTimeout       time.Duration
	ExecMode          string
	StatementCacheCap int
}

func NewPool(ctx context.Context, cfg Config) (*pgxpool.Pool, error) {
	pc, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	pc.MaxConns = cfg.MaxConns
	pc.MinConns = cfg.MinConns
	pc.MaxConnLifetime = cfg.ConnLifetime
	pc.MaxConnLifetimeJitter = cfg.ConnLifetime / 10
	pc.MaxConnIdleTime = 5 * time.Minute
	pc.HealthCheckPeriod = 30 * time.Second

	pc.ConnConfig.RuntimeParams["application_name"] = "space_api"
	pc.ConnConfig.RuntimeParams["timezone"] = "UTC"

	// pgx defaults to cache_statement, which prepares each distinct SQL text
	// and keeps an LRU per connection. Poolers vary in prepared-statement support,
	// so the query mode must be applied before creating the pool.
	switch cfg.ExecMode {
	case "", "cache_statement":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	case "cache_describe":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	case "describe_exec":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	case "exec":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	case "simple_protocol":
		pc.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	default:
		return nil, fmt.Errorf("unknown SPACE_DB_EXEC_MODE %q", cfg.ExecMode)
	}

	if cfg.StatementCacheCap > 0 {
		pc.ConnConfig.StatementCacheCapacity = cfg.StatementCacheCap
	}

	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
		if err != nil {
			rbErr := tx.Rollback(context.WithoutCancel(ctx))
			if rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				err = errors.Join(err, fmt.Errorf("rollback tx: %w", rbErr))
			}
		}
	}()

	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
