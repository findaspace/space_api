package contact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("contact: listing not found")
	ErrTooMany  = errors.New("contact: daily reveal limit reached")
)

type Contact struct {
	DisplayName string
	Phone       string // E.164
}

type Service struct {
	pool     *pgxpool.Pool
	dailyCap int
}

func NewService(pool *pgxpool.Pool, dailyCap int) *Service {
	return &Service{pool: pool, dailyCap: dailyCap}
}

// Reveal returns a published listing's owner, and records that this viewer
// saw it. The count and the insert are not one transaction on purpose: a
// viewer racing two requests could exceed the cap by one, which is harmless,
// and the simpler query is worth more than that exactness.
func (s *Service) Reveal(ctx context.Context, viewerID, spaceID uuid.UUID) (Contact, error) {
	var hostID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT host_id FROM spaces WHERE id = $1 AND status = 'published'`, spaceID).Scan(&hostID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	if err != nil {
		return Contact{}, fmt.Errorf("find listing: %w", err)
	}

	// An owner looking at their own listing costs nothing against the cap.
	if hostID != viewerID {
		var recent int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM contact_reveals WHERE viewer_id = $1 AND created_at > $2`,
			viewerID, time.Now().Add(-24*time.Hour)).Scan(&recent); err != nil {
			return Contact{}, fmt.Errorf("count reveals: %w", err)
		}
		if recent >= s.dailyCap {
			return Contact{}, ErrTooMany
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO contact_reveals (viewer_id, space_id, host_id) VALUES ($1, $2, $3)`,
			viewerID, spaceID, hostID); err != nil {
			return Contact{}, fmt.Errorf("record reveal: %w", err)
		}
	}

	var c Contact
	if err := s.pool.QueryRow(ctx,
		`SELECT display_name, phone_e164 FROM users WHERE id = $1`, hostID).Scan(&c.DisplayName, &c.Phone); err != nil {
		return Contact{}, fmt.Errorf("load owner: %w", err)
	}
	return c, nil
}
