package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const bookingColumns = `
	id, reference, space_id, host_id, guest_id,
	rental_mode::text, status::text,
	starts_on, ends_on, nights, months, guests, cancellation_policy::text,
	currency, subtotal_minor, discount_minor, fees_minor, total_minor, due_now_minor, quote,
	hold_expires_at, approved_at, confirmed_at, cancelled_at, cancelled_by::text,
	cancel_reason, refund_minor, created_at, updated_at`

func scan(row pgx.Row) (Booking, error) {
	var (
		b         Booking
		quote     []byte
		cancelled *string
	)
	err := row.Scan(
		&b.ID, &b.Reference, &b.SpaceID, &b.HostID, &b.GuestID,
		&b.Mode, &b.Status,
		&b.StartsOn, &b.EndsOn, &b.Nights, &b.Months, &b.Guests, &b.Policy,
		&b.Currency, &b.SubtotalMinor, &b.DiscountMinor, &b.FeesMinor,
		&b.TotalMinor, &b.DueNowMinor, &quote,
		&b.HoldExpiresAt, &b.ApprovedAt, &b.ConfirmedAt, &b.CancelledAt, &cancelled,
		&b.CancelReason, &b.RefundMinor, &b.CreatedAt, &b.UpdatedAt,
	)
	if err != nil {
		return Booking{}, err
	}

	if len(quote) > 0 {
		b.Quote = map[string]any{}
		if err := json.Unmarshal(quote, &b.Quote); err != nil {
			return Booking{}, fmt.Errorf("decode quote for %s: %w", b.ID, err)
		}
	}
	if cancelled != nil {
		a := Actor(*cancelled)
		b.CancelledBy = &a
	}
	return b, nil
}

func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "bookings_no_overlap":
		return ErrDatesTaken
	case "bookings_guest_not_host":
		return ErrOwnListing
	}
	return err
}

type HoldInput struct {
	SpaceID        uuid.UUID
	HostID         uuid.UUID
	GuestID        uuid.UUID
	Mode           Mode
	StartsOn       time.Time
	EndsOn         time.Time
	Nights         int
	Months         int
	Guests         int
	Currency       string
	SubtotalMinor  int64
	DiscountMinor  int64
	FeesMinor      int64
	TotalMinor     int64
	DueNowMinor    int64
	Quote          any
	IdempotencyKey string
	HoldFor        time.Duration
	Policy         Policy
}

// Hold creates a booking that blocks its dates. Concurrency is settled by the
// exclusion constraint, so this needs no advisory lock, no SELECT FOR UPDATE
// on the space, and no SERIALIZABLE retry loop.
func (s *Store) Hold(ctx context.Context, in HoldInput) (Booking, error) {
	quote, err := json.Marshal(in.Quote)
	if err != nil {
		return Booking{}, fmt.Errorf("encode quote: %w", err)
	}

	reference, err := NewReference()
	if err != nil {
		return Booking{}, err
	}

	var key *string
	if in.IdempotencyKey != "" {
		key = &in.IdempotencyKey
	}

	var out Booking
	err = postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		// Lapsed holds still satisfy the constraint predicate, because an index
		// predicate must be IMMUTABLE and so cannot call now(). Sweeping them
		// here makes expiry exact at the only moment it matters.
		if _, err := tx.Exec(ctx, `
			UPDATE bookings SET status = 'expired'
			WHERE space_id = $1 AND status = 'held' AND hold_expires_at <= now()`,
			in.SpaceID); err != nil {
			return fmt.Errorf("expire stale holds: %w", err)
		}

		// A replayed key returns the original booking. This has to run before
		// the insert: the insert would trip the exclusion constraint first and
		// report "dates taken" for what is really the same booking.
		if key != nil {
			existing, lookupErr := scan(tx.QueryRow(ctx,
				`SELECT `+bookingColumns+` FROM bookings
				 WHERE guest_id = $1 AND idempotency_key = $2`, in.GuestID, *key))
			if lookupErr == nil {
				out = existing
				return nil
			}
			if !errors.Is(lookupErr, pgx.ErrNoRows) {
				return fmt.Errorf("check idempotency key: %w", lookupErr)
			}
		}

		out, err = scan(tx.QueryRow(ctx, `
			INSERT INTO bookings (
				reference, space_id, host_id, guest_id, rental_mode, status,
				starts_on, ends_on, nights, months, guests,
				currency, subtotal_minor, discount_minor, fees_minor,
				total_minor, due_now_minor, quote,
				idempotency_key, hold_expires_at, cancellation_policy
			) VALUES (
				$1, $2, $3, $4, $5::rental_mode, 'held',
				$6, $7, $8, $9, $10,
				$11, $12, $13, $14,
				$15, $16, $17,
				$18, now() + $19::interval, $20::cancellation_policy
			)
			RETURNING `+bookingColumns,
			reference, in.SpaceID, in.HostID, in.GuestID, string(in.Mode),
			in.StartsOn, in.EndsOn, in.Nights, in.Months, in.Guests,
			in.Currency, in.SubtotalMinor, in.DiscountMinor, in.FeesMinor,
			in.TotalMinor, in.DueNowMinor, quote,
			key, in.HoldFor, string(in.Policy),
		))
		if err != nil {
			return mapPgError(err)
		}
		return nil
	})

	// A replayed idempotency key is not an error: return what the first call made.
	// Backstop for two concurrent first attempts with the same key: either
	// constraint may fire, so diagnose by looking the key up rather than by
	// inspecting which one it was.
	if err != nil && key != nil {
		if b, lookupErr := s.ByIdempotencyKey(ctx, in.GuestID, *key); lookupErr == nil {
			return b, nil
		}
	}
	if err != nil {
		return Booking{}, err
	}
	return out, nil
}

func (s *Store) ByIdempotencyKey(ctx context.Context, guestID uuid.UUID, key string) (Booking, error) {
	b, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE guest_id = $1 AND idempotency_key = $2`,
		guestID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get booking by key: %w", err)
	}
	return b, nil
}

// ByIDForParty returns a booking only to its guest or its host. A wrong party
// is indistinguishable from a missing row, which is deliberate.
func (s *Store) ByIDForParty(ctx context.Context, id, userID uuid.UUID) (Booking, error) {
	b, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings
		 WHERE id = $1 AND (guest_id = $2 OR host_id = $2)`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrNotFound
	}
	if err != nil {
		return Booking{}, fmt.Errorf("get booking: %w", err)
	}
	return b, nil
}

type TransitionInput struct {
	ID     uuid.UUID
	From   Status
	To     Status
	Actor  Actor
	Reason string
	Refund int64
	At     time.Time
}

// Transition applies a status change only if the row is still in the status the
// caller validated against. Zero rows means somebody moved it first.
func (s *Store) Transition(ctx context.Context, in TransitionInput) (Booking, error) {
	b, err := scan(s.pool.QueryRow(ctx, `
		UPDATE bookings SET
			status       = $3::booking_status,
			approved_at  = CASE WHEN $3::booking_status = 'approved'  THEN COALESCE(approved_at, now())  ELSE approved_at  END,
			confirmed_at = CASE WHEN $3::booking_status = 'confirmed' THEN COALESCE(confirmed_at, now()) ELSE confirmed_at END,
			cancelled_at = CASE WHEN $3::booking_status IN ('cancelled','declined') THEN COALESCE(cancelled_at, now()) ELSE cancelled_at END,
			cancelled_by = CASE WHEN $3::booking_status IN ('cancelled','declined') THEN $4::actor_kind ELSE cancelled_by END,
			cancel_reason = CASE WHEN $3::booking_status IN ('cancelled','declined') THEN $5 ELSE cancel_reason END,
			refund_minor = CASE WHEN $3::booking_status IN ('cancelled','declined') THEN $6 ELSE refund_minor END,
			hold_expires_at = CASE WHEN $3::booking_status = 'held' THEN hold_expires_at ELSE NULL END
		WHERE id = $1 AND status = $2::booking_status
		RETURNING `+bookingColumns,
		in.ID, string(in.From), string(in.To), string(in.Actor), in.Reason, in.Refund))
	if errors.Is(err, pgx.ErrNoRows) {
		return Booking{}, ErrConflict
	}
	if err != nil {
		return Booking{}, fmt.Errorf("transition booking: %w", mapPgError(err))
	}
	return b, nil
}

// ExpireHolds is what the Phase 8 worker will call on a schedule. Hold() covers
// correctness already; this keeps stale rows from lingering in host calendars.
func (s *Store) ExpireHolds(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE bookings SET status = 'expired'
		 WHERE status = 'held' AND hold_expires_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("expire holds: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ListForGuest(ctx context.Context, guestID uuid.UUID, limit, offset int32) ([]Booking, error) {
	return s.list(ctx, `WHERE guest_id = $1`, guestID, limit, offset)
}

func (s *Store) ListForHost(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Booking, error) {
	return s.list(ctx, `WHERE host_id = $1`, hostID, limit, offset)
}

func (s *Store) list(ctx context.Context, where string, id uuid.UUID, limit, offset int32) ([]Booking, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+bookingColumns+` FROM bookings `+where+
			` ORDER BY created_at DESC LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list bookings: %w", err)
	}
	defer rows.Close()

	out := []Booking{}
	for rows.Next() {
		b, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan booking: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
