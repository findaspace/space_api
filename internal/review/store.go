package review

import (
	"context"
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

func (s *Store) Stay(ctx context.Context, bookingID uuid.UUID) (Stay, error) {
	var st Stay
	err := s.pool.QueryRow(ctx, `
		SELECT id, space_id, guest_id, host_id, status::text, ends_on
		FROM bookings WHERE id = $1`, bookingID).
		Scan(&st.BookingID, &st.SpaceID, &st.GuestID, &st.HostID, &st.Status, &st.EndsOn)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stay{}, ErrNotFound
	}
	if err != nil {
		return Stay{}, fmt.Errorf("get stay: %w", err)
	}
	return st, nil
}

// Submit inserts a review and, if the other side has already written theirs,
// reveals both in the same transaction. Double-blind means the moment the
// second review lands is the moment both become visible, never before.
func (s *Store) Submit(ctx context.Context, r Review) (Review, error) {
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO reviews (booking_id, space_id, author_id, subject_id, author_role, rating, body)
			VALUES ($1, $2, $3, $4, $5::review_role, $6, $7)
			RETURNING id, created_at`,
			r.BookingID, r.SpaceID, r.AuthorID, r.SubjectID, string(r.AuthorRole),
			r.Rating, r.Body).Scan(&r.ID, &r.CreatedAt)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.ConstraintName == "reviews_one_per_side" {
				return ErrAlreadyWritten
			}
			return fmt.Errorf("insert review: %w", err)
		}

		var both int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM reviews WHERE booking_id = $1`, r.BookingID).Scan(&both); err != nil {
			return fmt.Errorf("count reviews: %w", err)
		}
		if both == 2 {
			now := time.Now()
			if _, err := tx.Exec(ctx,
				`UPDATE reviews SET revealed_at = $2 WHERE booking_id = $1 AND revealed_at IS NULL`,
				r.BookingID, now); err != nil {
				return fmt.Errorf("reveal reviews: %w", err)
			}
			r.RevealedAt = &now
		}
		return nil
	})
	return r, err
}

// RevealExpired is the other half of double-blind: a review nobody answered is
// published once the window closes, so one silent party cannot bury the other.
func (s *Store) RevealExpired(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE reviews r SET revealed_at = now()
		FROM bookings b
		WHERE r.booking_id = b.id
		  AND r.revealed_at IS NULL
		  AND b.ends_on + interval '14 days' <= now()`)
	if err != nil {
		return 0, fmt.Errorf("reveal expired reviews: %w", err)
	}
	return tag.RowsAffected(), nil
}

type Summary struct {
	Count   int     `json:"count"`
	Average float64 `json:"average"`
}

// ForSpace returns the public guest reviews and their summary. Computed per
// request rather than cached: it is one indexed aggregate, and a cached average
// is one more number that can drift from the rows it summarizes.
func (s *Store) ForSpace(ctx context.Context, spaceID uuid.UUID, limit int32) ([]Review, Summary, error) {
	var sum Summary
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(round(avg(rating)::numeric, 2), 0)::float8
		FROM reviews
		WHERE space_id = $1 AND author_role = 'guest' AND revealed_at IS NOT NULL`, spaceID).
		Scan(&sum.Count, &sum.Average); err != nil {
		return nil, Summary{}, fmt.Errorf("summarize reviews: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, booking_id, space_id, author_id, subject_id, author_role::text,
		       rating, body, revealed_at, created_at
		FROM reviews
		WHERE space_id = $1 AND author_role = 'guest' AND revealed_at IS NOT NULL
		ORDER BY revealed_at DESC LIMIT $2`, spaceID, limit)
	if err != nil {
		return nil, Summary{}, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()

	out := []Review{}
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.BookingID, &r.SpaceID, &r.AuthorID, &r.SubjectID,
			&r.AuthorRole, &r.Rating, &r.Body, &r.RevealedAt, &r.CreatedAt); err != nil {
			return nil, Summary{}, fmt.Errorf("scan review: %w", err)
		}
		out = append(out, r)
	}
	return out, sum, rows.Err()
}
