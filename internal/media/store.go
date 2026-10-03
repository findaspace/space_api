package media

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
	"github.com/samnart1/space_api/internal/platform/postgres/dbgen"
)

type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

func toDomain(row dbgen.SpaceMedium) Media {
	return Media{
		ID:          row.ID,
		SpaceID:     row.SpaceID,
		ObjectKey:   row.ObjectKey,
		ContentType: row.ContentType,
		ByteSize:    row.ByteSize,
		Width:       int(row.Width),
		Height:      int(row.Height),
		Position:    int(row.Position),
		Status:      Status(row.Status),
		CreatedAt:   row.CreatedAt,
		CardKey:     row.CardKey,
	}
}

func (s *Store) Create(ctx context.Context, id, spaceID uuid.UUID, key, contentType string, position int32) (Media, error) {
	row, err := s.q.CreateMedia(ctx, dbgen.CreateMediaParams{
		ID:          id,
		SpaceID:     spaceID,
		ObjectKey:   key,
		ContentType: contentType,
		Position:    position,
	})
	if err != nil {
		return Media{}, fmt.Errorf("create media: %w", err)
	}
	return toDomain(row), nil
}

func (s *Store) Get(ctx context.Context, id, spaceID uuid.UUID) (Media, error) {
	row, err := s.q.GetMedia(ctx, dbgen.GetMediaParams{ID: id, SpaceID: spaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("get media: %w", err)
	}
	return toDomain(row), nil
}

func (s *Store) MarkReady(ctx context.Context, id, spaceID uuid.UUID, key, cardKey, contentType string,
	size int64, w, h int) (Media, error) {
	row, err := s.q.MarkMediaReady(ctx, dbgen.MarkMediaReadyParams{
		ID:          id,
		SpaceID:     spaceID,
		ObjectKey:   key,
		CardKey:     cardKey,
		ContentType: contentType,
		ByteSize:    size,
		Width:       int32(w),
		Height:      int32(h),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("mark media ready: %w", err)
	}
	return toDomain(row), nil
}

func (s *Store) ListReady(ctx context.Context, spaceID uuid.UUID) ([]Media, error) {
	rows, err := s.q.ListMedia(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("list media: %w", err)
	}
	out := make([]Media, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// CountReady satisfies listing.MediaCounter.
func (s *Store) CountReady(ctx context.Context, spaceID uuid.UUID) (int, error) {
	n, err := s.q.CountReadyMedia(ctx, spaceID)
	if err != nil {
		return 0, fmt.Errorf("count ready media: %w", err)
	}
	return int(n), nil
}

func (s *Store) Count(ctx context.Context, spaceID uuid.UUID) (int, error) {
	n, err := s.q.CountMedia(ctx, spaceID)
	if err != nil {
		return 0, fmt.Errorf("count media: %w", err)
	}
	return int(n), nil
}

func (s *Store) NextPosition(ctx context.Context, spaceID uuid.UUID) (int32, error) {
	n, err := s.q.NextMediaPosition(ctx, spaceID)
	if err != nil {
		return 0, fmt.Errorf("next media position: %w", err)
	}
	return n, nil
}

// Delete removes the row and returns the key so the caller can drop the object.
func (s *Store) Delete(ctx context.Context, id, spaceID uuid.UUID) (string, error) {
	var key string
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var status string
		if err := tx.QueryRow(ctx, "SELECT status FROM spaces WHERE id=$1 FOR UPDATE", spaceID).Scan(&status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var mediaStatus string
		if err := tx.QueryRow(ctx, "SELECT object_key,status FROM space_media WHERE id=$1 AND space_id=$2 FOR UPDATE", id, spaceID).Scan(&key, &mediaStatus); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if status == "published" && mediaStatus == "ready" {
			var count int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM space_media WHERE space_id=$1 AND status='ready'", spaceID).Scan(&count); err != nil {
				return err
			}
			if count <= 1 {
				return ErrLastPhoto
			}
		}
		_, err := tx.Exec(ctx, "DELETE FROM space_media WHERE id=$1 AND space_id=$2", id, spaceID)
		return err
	})
	return key, err
}

// Reorder rewrites every position in one transaction. Partial reordering would
// leave the gallery in an order nobody chose.
func (s *Store) Reorder(ctx context.Context, spaceID uuid.UUID, ids []uuid.UUID) error {
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM spaces WHERE id=$1 FOR UPDATE", spaceID).Scan(&locked); err != nil {
			return err
		}
		q := s.q.WithTx(tx)
		existing, err := q.ListMedia(ctx, spaceID)
		if err != nil {
			return err
		}
		if len(existing) != len(ids) {
			return ErrNotFound
		}
		owned := make(map[uuid.UUID]bool, len(existing))
		for _, m := range existing {
			owned[m.ID] = true
		}
		for _, id := range ids {
			if !owned[id] {
				return ErrNotFound
			}
			delete(owned, id)
		}
		for i, id := range ids {
			if err := q.SetMediaPosition(ctx, dbgen.SetMediaPositionParams{
				ID: id, SpaceID: spaceID, Position: int32(i),
			}); err != nil {
				return fmt.Errorf("set position: %w", err)
			}
		}
		return nil
	})
}

func (s *Store) MarkFailed(ctx context.Context, id, spaceID uuid.UUID, reason string) error {
	if err := s.q.MarkMediaFailed(ctx, dbgen.MarkMediaFailedParams{
		ID: id, SpaceID: spaceID, FailureReason: reason,
	}); err != nil {
		return fmt.Errorf("mark media failed: %w", err)
	}
	return nil
}

func (s *Store) Abandoned(ctx context.Context, olderThan time.Duration, limit int32) ([]Media, error) {
	rows, err := s.q.ListAbandonedMedia(ctx, dbgen.ListAbandonedMediaParams{
		Cutoff:   time.Now().Add(-olderThan),
		RowLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list abandoned media: %w", err)
	}
	out := make([]Media, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == constraint
}

// Reserve serialises upload tickets per space so simultaneous requests cannot exceed the photo limit.
func (s *Store) Reserve(ctx context.Context, id, spaceID uuid.UUID, key, contentType string, maxPhotos int) (Media, error) {
	var result Media
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var locked uuid.UUID
		if err := tx.QueryRow(ctx, "SELECT id FROM spaces WHERE id=$1 FOR UPDATE", spaceID).Scan(&locked); err != nil {
			return err
		}
		q := s.q.WithTx(tx)
		count, err := q.CountMedia(ctx, spaceID)
		if err != nil {
			return err
		}
		if count >= int64(maxPhotos) {
			return ErrTooMany
		}
		position, err := q.NextMediaPosition(ctx, spaceID)
		if err != nil {
			return err
		}
		row, err := q.CreateMedia(ctx, dbgen.CreateMediaParams{ID: id, SpaceID: spaceID, ObjectKey: key, ContentType: contentType, Position: position})
		if err != nil {
			return err
		}
		result = toDomain(row)
		return nil
	})
	return result, err
}
