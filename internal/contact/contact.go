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

// Reveal serialises each viewer's cap check and records every successful reveal.
// Reopening the same space does not consume another unique-space allowance.
func (s *Service) Reveal(ctx context.Context, viewerID, spaceID uuid.UUID) (Contact, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Contact{}, err
	}
	defer tx.Rollback(ctx)
	var locked uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT id FROM users WHERE id=$1 AND status='active' FOR UPDATE", viewerID).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Contact{}, ErrNotFound
		}
		return Contact{}, err
	}
	var hostID uuid.UUID
	var c Contact
	err = tx.QueryRow(ctx, "SELECT s.host_id,u.display_name,u.phone_e164 FROM spaces s JOIN users u ON u.id=s.host_id WHERE s.id=$1 AND s.status='published' AND u.status='active'", spaceID).Scan(&hostID, &c.DisplayName, &c.Phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return Contact{}, ErrNotFound
	}
	if err != nil {
		return Contact{}, fmt.Errorf("find listing: %w", err)
	}
	if hostID != viewerID {
		var recent int
		var already bool
		since := time.Now().Add(-24 * time.Hour)
		if err = tx.QueryRow(ctx, "SELECT count(DISTINCT space_id),COALESCE(bool_or(space_id=$3),false) FROM contact_reveals WHERE viewer_id=$1 AND created_at>$2", viewerID, since, spaceID).Scan(&recent, &already); err != nil {
			return Contact{}, err
		}
		if !already && recent >= s.dailyCap {
			return Contact{}, ErrTooMany
		}
		if _, err = tx.Exec(ctx, "INSERT INTO contact_reveals(viewer_id,space_id,host_id) VALUES($1,$2,$3)", viewerID, spaceID, hostID); err != nil {
			return Contact{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Contact{}, err
	}
	return c, nil
}
