package search

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Search(ctx context.Context, q Query) ([]Result, error) {
	sql, args := build(q)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	defer rows.Close()

	out := []Result{}
	for rows.Next() {
		var (
			r        Result
			distance *int
			epoch    int64
		)
		if err := rows.Scan(
			&r.ID, &r.Slug, &r.Title, &r.SpaceType, &r.RentalMode,
			&r.PriceMinor, &r.Currency, &r.PricePeriod,
			&r.Locality, &r.Latitude, &r.Longitude,
			&r.PhotoCount, &r.ReviewCount, &r.Rating, &r.CoverKey,
			&distance, &epoch, &r.Score,
		); err != nil {
			return nil, fmt.Errorf("scan result: %w", err)
		}
		r.DistanceM = distance

		// The cursor carries whatever the active sort ordered by, so the next
		// page resumes from the right column without the client knowing which.
		switch q.Sort {
		case SortPriceAsc, SortPriceDesc:
			r.Score = r.PriceMinor
		case SortDistance:
			if distance != nil {
				r.Score = int64(*distance)
			}
		case SortNewest:
			r.Score = epoch
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RefreshStats recomputes the denormalized ranking columns and reports how many
// listings actually changed.
func (s *Store) RefreshStats(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, RefreshStatsSQL)
	if err != nil {
		return 0, fmt.Errorf("refresh search stats: %w", err)
	}
	return tag.RowsAffected(), nil
}
