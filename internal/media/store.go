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

func (s *Store) MarkReady(ctx context.Context, id, spaceID uuid.UUID, key, contentType string, size int64, w, h int) (Media, error) {
	row, err := s.q.MarkMediaReady(ctx, dbgen.MarkMediaReadyParams{
		ID:          id,
		SpaceID:     spaceID,
		ObjectKey:   key,
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
	key, err := s.q.DeleteMedia(ctx, dbgen.DeleteMediaParams{ID: id, SpaceID: spaceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("delete media: %w", err)
	}
	return key, nil
}

// Reorder rewrites every position in one transaction. Partial reordering would
// leave the gallery in an order nobody chose.
func (s *Store) Reorder(ctx context.Context, spaceID uuid.UUID, ids []uuid.UUID) error {
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
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
