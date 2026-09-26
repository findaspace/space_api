package identity

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These run under `make itest` against a real database, because the property
// being tested is how Postgres re-checks a WHERE clause after waiting on a row
// lock. A mock would only test the mock.

func newGraceStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("SPACE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SPACE_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewStore(pool), pool
}

// seedSession creates a user with one live session and returns the session's
// refresh hash, cleaning both up when the test ends.
func seedSession(t *testing.T, pool *pgxpool.Pool) []byte {
	t.Helper()
	ctx := context.Background()

	phone := fmt.Sprintf("+23320%07d", time.Now().UnixNano()%10_000_000)
	var userID uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (phone_e164, phone_verified_at) VALUES ($1, now()) RETURNING id`,
		phone).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	hash := []byte("grace-test-" + uuid.NewString())
	if _, err := pool.Exec(ctx, `
		INSERT INTO sessions (user_id, family_id, refresh_hash, user_agent, request_ip, expires_at)
		VALUES ($1, $2, $3, '', '', now() + interval '1 day')`,
		userID, uuid.New(), hash); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM sessions WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})
	return hash
}

// The bug this window fixes: space_web fires several requests in parallel, and
// when an access token expires they all refresh with the same token at once.
// Without the window, every one after the first looks like a stolen token
// being replayed and revokes the family, signing out a user who did nothing.
func TestConcurrentRefreshesWithinGraceAllSucceed(t *testing.T) {
	store, pool := newGraceStore(t)
	old := seedSession(t, pool)

	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)

	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			next := []byte(fmt.Sprintf("grace-next-%d-%s", i, uuid.NewString()))
			_, _, errs[i] = store.Rotate(context.Background(), old, next, "", "",
				24*time.Hour, 90*24*time.Hour, 30*time.Second)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("refresh %d failed: %v", i, err)
		}
	}
}

// The window must not weaken theft detection. A token replayed after the
// window has closed is treated exactly as before.
func TestReplayAfterGraceIsStillRejected(t *testing.T) {
	store, pool := newGraceStore(t)
	old := seedSession(t, pool)

	if _, err := pool.Exec(context.Background(),
		`UPDATE sessions SET rotated_at = now() - interval '5 minutes' WHERE refresh_hash = $1`, old); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.Rotate(context.Background(), old, []byte("late-"+uuid.NewString()), "", "",
		24*time.Hour, 90*24*time.Hour, 30*time.Second)
	if !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("err = %v, want ErrSessionInvalid so DiagnoseRefresh revokes the family", err)
	}
}

// Using a token inside the window must not reset the window, or a thief could
// keep a stolen token alive indefinitely by using it every few seconds.
func TestGraceDoesNotSlide(t *testing.T) {
	store, pool := newGraceStore(t)
	old := seedSession(t, pool)
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`UPDATE sessions SET rotated_at = now() - interval '20 seconds' WHERE refresh_hash = $1`, old); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Rotate(ctx, old, []byte("slide-"+uuid.NewString()), "", "",
		24*time.Hour, 90*24*time.Hour, 30*time.Second); err != nil {
		t.Fatalf("inside the window: %v", err)
	}

	var age float64
	if err := pool.QueryRow(ctx,
		`SELECT extract(epoch FROM now() - rotated_at) FROM sessions WHERE refresh_hash = $1`, old).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age < 19 {
		t.Errorf("rotated_at moved forward to %.0fs ago; the window slid", age)
	}
}
