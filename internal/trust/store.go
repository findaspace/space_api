// Package trust owns reports, communication boundaries and account closure.
package trust

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

var (
	ErrNotFound    = errors.New("trust: not found")
	ErrInvalid     = errors.New("trust: invalid report")
	ErrObligations = errors.New("trust: resolve active bookings or transfer administrator access first")
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Report struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	ID        uuid.UUID `json:"id"`
	SpaceID   uuid.UUID `json:"space_id"`
	Reason    string    `json:"reason"`
	Details   string    `json:"details"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

func ValidReport(reason, details string) bool {
	switch reason {
	case "suspected_scam", "unavailable", "incorrect_details", "abuse", "other":
		return utf8.RuneCountInString(details) <= 2000
	default:
		return false
	}
}
func audit(ctx context.Context, tx pgx.Tx, actor uuid.UUID, action string, subject uuid.UUID) error {
	_, err := tx.Exec(ctx, "INSERT INTO trust_audit(actor_id,action,subject_id) VALUES($1,$2,$3)", actor, action, subject)
	return err
}
func (s *Store) Report(ctx context.Context, actor, space uuid.UUID, reason, details string) (uuid.UUID, error) {
	details = strings.TrimSpace(details)
	if !ValidReport(reason, details) {
		return uuid.Nil, ErrInvalid
	}
	var id uuid.UUID
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var live uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM spaces WHERE id=$1 AND status='published' FOR SHARE", space).Scan(&live); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		// Lock the reporter to serialise duplicates without overwriting evidence.
		if err := tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 AND status='active' FOR UPDATE", actor).Scan(&live); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO listing_reports(reporter_id,space_id,reason,details) VALUES($1,$2,$3,$4)
   ON CONFLICT(reporter_id,space_id) WHERE status='open' DO NOTHING RETURNING id`, actor, space, reason, details).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return tx.QueryRow(ctx, "SELECT id FROM listing_reports WHERE reporter_id=$1 AND space_id=$2 AND status='open'", actor, space).Scan(&id)
		}
		if err != nil {
			return err
		}
		return audit(ctx, tx, actor, "listing_reported", id)
	})
	return id, err
}
func (s *Store) Reports(ctx context.Context) ([]Report, error) {
	rows, err := s.pool.Query(ctx, "SELECT r.id,r.space_id,r.reason,r.details,r.status,r.created_at,s.title,s.slug FROM listing_reports r JOIN spaces s ON s.id=r.space_id WHERE r.status='open' ORDER BY r.created_at,r.id LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Report{}
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.ID, &r.SpaceID, &r.Reason, &r.Details, &r.Status, &r.CreatedAt, &r.Title, &r.Slug); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Review(ctx context.Context, actor, id uuid.UUID, status, note string, withdraw bool) error {
	if (status != "resolved" && status != "dismissed") || (withdraw && status != "resolved") || utf8.RuneCountInString(note) > 2000 {
		return ErrInvalid
	}
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "UPDATE listing_reports SET status=$2,review_note=$3,reviewer_id=$4,reviewed_at=now() WHERE id=$1 AND status='open'", id, status, strings.TrimSpace(note), actor)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if withdraw {
			if status != "resolved" {
				return ErrInvalid
			}
			var space uuid.UUID
			if err := tx.QueryRow(ctx, "SELECT space_id FROM listing_reports WHERE id=$1", id).Scan(&space); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "UPDATE spaces SET status='suspended' WHERE id=$1 AND status='published'", space); err != nil {
				return err
			}
			if err := audit(ctx, tx, actor, "listing_withdrawn", space); err != nil {
				return err
			}
		}
		return audit(ctx, tx, actor, "report_reviewed", id)
	})
}

type Boundary struct {
	Blocked          bool `json:"blocked"`
	MessagingAllowed bool `json:"messaging_allowed"`
}

func other(ctx context.Context, tx pgx.Tx, actor, thread uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, "SELECT CASE WHEN guest_id=$2 THEN host_id ELSE guest_id END FROM threads WHERE id=$1 AND (guest_id=$2 OR host_id=$2)", thread, actor).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrNotFound
	}
	return id, err
}
func (s *Store) Boundary(ctx context.Context, actor, thread uuid.UUID) (Boundary, error) {
	var b Boundary
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		target, err := other(ctx, tx, actor, thread)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM user_blocks WHERE blocker_id=$1 AND blocked_id=$2),
   NOT EXISTS(SELECT 1 FROM user_blocks WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1)) AND
   (SELECT count(*)=2 FROM users WHERE id IN ($1,$2) AND status='active')`, actor, target).Scan(&b.Blocked, &b.MessagingAllowed)
	})
	return b, err
}
func (s *Store) Block(ctx context.Context, actor, thread uuid.UUID, block bool) error {
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		target, err := other(ctx, tx, actor, thread)
		if err != nil {
			return err
		}
		// Same lock as the database triggers, including a no-op unblock.
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended(least($1::text,$2::text)||':'||greatest($1::text,$2::text),0))", actor, target); err != nil {
			return err
		}
		query := "INSERT INTO user_blocks(blocker_id,blocked_id) VALUES($1,$2) ON CONFLICT DO NOTHING"
		action := "user_blocked"
		if !block {
			query = "DELETE FROM user_blocks WHERE blocker_id=$1 AND blocked_id=$2"
			action = "user_unblocked"
		}
		tag, err := tx.Exec(ctx, query, actor, target)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		return audit(ctx, tx, actor, action, target)
	})
}
