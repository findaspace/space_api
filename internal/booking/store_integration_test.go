package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var phoneSeq atomic.Int64

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

// fixture creates a host, a guest and a published space, and removes them after.
func fixture(t *testing.T, pool *pgxpool.Pool) (spaceID, hostID, guestID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	if err := pool.QueryRow(ctx,
		`INSERT INTO users (phone_e164,phone_verified_at) VALUES ($1,now()) RETURNING id`, testPhone()).Scan(&hostID); err != nil {
		t.Fatalf("create host: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (phone_e164,phone_verified_at) VALUES ($1,now()) RETURNING id`, testPhone()).Scan(&guestID); err != nil {
		t.Fatalf("create guest: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO spaces (
			host_id, slug, space_type, rental_mode, status, title, description,
			base_price_minor, price_period, latitude, longitude, locality, published_at
		) VALUES ($1, $2, 'apartment', 'nightly', 'published', 'Integration test apartment',
			'A space used by the booking integration tests only.',
			20000, 'night', 5.6, -0.18, 'Accra', now())
		RETURNING id`, hostID, "itest-"+uuid.NewString()).Scan(&spaceID); err != nil {
		t.Fatalf("create space: %v", err)
	}

	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM bookings WHERE space_id = $1`, spaceID)
		_, _ = pool.Exec(ctx, `DELETE FROM spaces WHERE id = $1`, spaceID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{hostID, guestID})
	})
	return spaceID, hostID, guestID
}

// testPhone returns a unique, well-formed Ghanaian number: +233 plus exactly
// nine digits, which is what the users CHECK constraint requires.
func testPhone() string {
	n := (time.Now().UnixNano()/1000 + phoneSeq.Add(1)) % 1_000_000_000
	return fmt.Sprintf("+233%09d", n)
}
func holdInput(spaceID, hostID, guestID uuid.UUID, from, to time.Time) HoldInput {
	return HoldInput{
		SpaceID: spaceID, HostID: hostID, GuestID: guestID,
		Mode: ModeNightly, StartsOn: from, EndsOn: to,
		Nights: int(to.Sub(from) / (24 * time.Hour)), Guests: 2,
		Currency: "GHS", SubtotalMinor: 60000, TotalMinor: 60000, DueNowMinor: 60000,
		Quote: map[string]any{"mode": "nightly"}, Policy: PolicyModerate,
		HoldFor: 15 * time.Minute,
	}
}

func day(offset int) time.Time {
	y, m, d := time.Now().UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
}

// The whole reason this phase exists. Twenty concurrent guests, one set of
// dates, exactly one winner, and no application-level locking anywhere.
func TestConcurrentHoldsExactlyOneWins(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, _ := fixture(t, pool)

	const attempts = 20
	guests := make([]uuid.UUID, attempts)
	ctx := context.Background()
	for i := range guests {
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (phone_e164,phone_verified_at) VALUES ($1,now()) RETURNING id`,
			testPhone()).Scan(&guests[i]); err != nil {
			t.Fatalf("create guest %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM bookings WHERE guest_id = ANY($1)`, guests)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = ANY($1)`, guests)
	})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		taken   int
		unknown []error
	)

	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start

			_, err := store.Hold(context.Background(),
				holdInput(spaceID, hostID, guests[i], day(30), day(33)))

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrDatesTaken):
				taken++
			default:
				unknown = append(unknown, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if len(unknown) > 0 {
		t.Fatalf("unexpected errors: %v", unknown)
	}
	if wins != 1 {
		t.Errorf("winners = %d, want exactly 1", wins)
	}
	if taken != attempts-1 {
		t.Errorf("rejected = %d, want %d", taken, attempts-1)
	}
}

func TestAdjacentDatesDoNotConflict(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, guestID := fixture(t, pool)
	ctx := context.Background()

	if _, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(40), day(43))); err != nil {
		t.Fatalf("first hold: %v", err)
	}
	// Check-in on the previous booking's check-out day is not a conflict.
	if _, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(43), day(45))); err != nil {
		t.Fatalf("adjacent hold rejected: %v", err)
	}
}

func TestCancelledBookingReleasesDates(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, guestID := fixture(t, pool)
	ctx := context.Background()

	first, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(50), day(53)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(50), day(53))); !errors.Is(err, ErrDatesTaken) {
		t.Fatalf("err = %v, want ErrDatesTaken", err)
	}

	if _, err := store.Transition(ctx, TransitionInput{
		ID: first.ID, From: StatusHeld, To: StatusCancelled,
		Actor: ActorGuest, Reason: "changed plans",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(50), day(53))); err != nil {
		t.Fatalf("dates not released after cancellation: %v", err)
	}
}

func TestExpiredHoldIsSweptOnNextHold(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, guestID := fixture(t, pool)
	ctx := context.Background()

	in := holdInput(spaceID, hostID, guestID, day(60), day(62))
	in.HoldFor = -time.Minute // already lapsed
	if _, err := store.Hold(ctx, in); err != nil {
		t.Fatal(err)
	}

	// The lapsed hold still satisfies the constraint predicate; the sweep inside
	// Hold is what frees the dates.
	if _, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(60), day(62))); err != nil {
		t.Fatalf("expired hold still blocking: %v", err)
	}
}

func TestIdempotencyKeyReturnsTheSameBooking(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, guestID := fixture(t, pool)
	ctx := context.Background()

	in := holdInput(spaceID, hostID, guestID, day(70), day(72))
	in.IdempotencyKey = uuid.NewString()

	first, err := store.Hold(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Hold(ctx, in)
	if err != nil {
		t.Fatalf("replay returned an error: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("replay created a second booking: %s vs %s", first.ID, second.ID)
	}
}

func TestHostCannotBookOwnListing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, _ := fixture(t, pool)

	in := holdInput(spaceID, hostID, hostID, day(80), day(82))
	if _, err := store.Hold(context.Background(), in); !errors.Is(err, ErrOwnListing) {
		t.Fatalf("err = %v, want ErrOwnListing", err)
	}
}

func TestTransitionIsOptimistic(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	spaceID, hostID, guestID := fixture(t, pool)
	ctx := context.Background()

	b, err := store.Hold(ctx, holdInput(spaceID, hostID, guestID, day(90), day(92)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Transition(ctx, TransitionInput{
		ID: b.ID, From: StatusHeld, To: StatusCancelled, Actor: ActorGuest,
	}); err != nil {
		t.Fatal(err)
	}
	// Second attempt reads a status that no longer matches.
	if _, err := store.Transition(ctx, TransitionInput{
		ID: b.ID, From: StatusHeld, To: StatusConfirmed, Actor: ActorSystem,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}
