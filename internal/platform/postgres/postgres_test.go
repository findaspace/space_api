package postgres

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPoolAppliesExecModeBeforeConnecting(t *testing.T) {
	url := os.Getenv("SPACE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPACE_TEST_DATABASE_URL not set")
	}
	pool, err := NewPool(context.Background(), Config{URL: url, MaxConns: 2, MinConns: 1, ConnLifetime: time.Minute, PingTimeout: time.Second * 10, ExecMode: "exec", StatementCacheCap: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	cfg := pool.Config().ConnConfig
	if cfg.DefaultQueryExecMode != pgx.QueryExecModeExec || cfg.StatementCacheCapacity != 16 {
		t.Fatal("pool ignored execution/cache configuration")
	}
	var value int
	if err := pool.QueryRow(context.Background(), "SELECT $1::int", 42).Scan(&value); err != nil || value != 42 {
		t.Fatalf("configured query failed: %v", err)
	}
}
func TestPoolRejectsUnknownModeBeforeConnecting(t *testing.T) {
	_, err := NewPool(context.Background(), Config{URL: "postgres://localhost:1/test", MaxConns: 1, ExecMode: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "SPACE_DB_EXEC_MODE") {
		t.Fatalf("expected mode error, got %v", err)
	}
}
