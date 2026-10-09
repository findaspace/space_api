package ledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var seq atomic.Int64

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("SPACE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPACE_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testHost(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	n := (time.Now().UnixNano()/1000 + seq.Add(1)) % 1_000_000_000
	var id uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (phone_e164) VALUES ($1) RETURNING id`,
		fmt.Sprintf("+233%09d", n)).Scan(&id); err != nil {
		t.Fatalf("create host: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `ALTER TABLE ledger_entries DISABLE TRIGGER ledger_entries_immutable`)
		_, _ = pool.Exec(ctx, `
			DELETE FROM ledger_entries WHERE transaction_id IN (
				SELECT transaction_id FROM ledger_entries WHERE party_id = $1
			)`, id)
		_, _ = pool.Exec(ctx, `
			DELETE FROM ledger_transactions WHERE id NOT IN (
				SELECT DISTINCT transaction_id FROM ledger_entries
			)`)
		_, _ = pool.Exec(ctx, `ALTER TABLE ledger_entries ENABLE TRIGGER ledger_entries_immutable`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func TestPostAndBalance(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	host := testHost(t, pool)
	ctx := context.Background()

	ref := "charge-" + uuid.NewString()
	if _, err := store.Post(ctx, Charge(nil, host, "GHS", ref, 90000, 10000)); err != nil {
		t.Fatalf("post charge: %v", err)
	}

	owed, err := store.Balance(ctx, AccountHostPayable, &host)
	if err != nil {
		t.Fatal(err)
	}
	if owed != 90000 {
		t.Errorf("host owed = %d, want 90000", owed)
	}

	if _, err := store.Post(ctx, Payout(host, "GHS", "payout-"+uuid.NewString(), 90000)); err != nil {
		t.Fatalf("post payout: %v", err)
	}

	owed, err = store.Balance(ctx, AccountHostPayable, &host)
	if err != nil {
		t.Fatal(err)
	}
	if owed != 0 {
		t.Errorf("host owed after payout = %d, want 0", owed)
	}
}

// The database refuses an unbalanced posting even if application validation is
// bypassed. This is the guarantee the whole phase rests on.
func TestDatabaseRejectsUnbalancedPosting(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var txID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO ledger_transactions (kind, currency, memo)
		VALUES ('charge', 'GHS', 'deliberately broken') RETURNING id`).Scan(&txID); err != nil {
		t.Fatal(err)
	}

	// Inserted directly, skipping Post and its Validate call.
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (transaction_id, account, direction, amount_minor)
		VALUES ($1, 'psp_clearing', 'debit', 100000),
		       ($1, 'host_payable', 'credit', 90000)`, txID); err != nil {
		t.Fatalf("inserts should succeed; the check is deferred: %v", err)
	}

	// The trigger fires here, at COMMIT.
	err = tx.Commit(ctx)
	if err == nil {
		t.Fatal("commit succeeded with an unbalanced posting")
	}
	if !strings.Contains(err.Error(), "out of balance") {
		t.Fatalf("err = %v, want an out-of-balance error", err)
	}
}

func TestEntriesAreAppendOnly(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	host := testHost(t, pool)
	ctx := context.Background()

	txID, err := store.Post(ctx, Charge(nil, host, "GHS", "imm-"+uuid.NewString(), 90000, 10000))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE ledger_entries SET amount_minor = 1 WHERE transaction_id = $1`, txID); err == nil {
		t.Error("update succeeded; entries must be immutable")
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM ledger_entries WHERE transaction_id = $1`, txID); err == nil {
		t.Error("delete succeeded; entries must be immutable")
	}
}

// A replayed webhook must not post the same charge twice.
func TestExternalRefIsUnique(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	host := testHost(t, pool)
	ctx := context.Background()

	ref := "dup-" + uuid.NewString()
	if _, err := store.Post(ctx, Charge(nil, host, "GHS", ref, 90000, 10000)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Post(ctx, Charge(nil, host, "GHS", ref, 90000, 10000)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestTrialBalanceIsZero(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	host := testHost(t, pool)
	ctx := context.Background()

	if _, err := store.Post(ctx, Charge(nil, host, "GHS", "tb-"+uuid.NewString(), 90000, 10000)); err != nil {
		t.Fatal(err)
	}

	net, err := store.TrialBalance(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if net != 0 {
		t.Errorf("trial balance = %d, want 0", net)
	}
}
