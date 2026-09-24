package geo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres"
)

var ErrNotFound = errors.New("geo: space not found")

type Landmark struct {
	ID       uuid.UUID
	Name     string
	Kind     string
	Locality string
	Lat      float64
	Lng      float64
}

type Walk struct {
	Landmark Landmark
	Seconds  int
	Metres   int
}

type SpacePoint struct {
	ID  uuid.UUID
	Lat float64
	Lng float64
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

func (s *Store) SpacePoint(ctx context.Context, spaceID uuid.UUID) (SpacePoint, error) {
	var p SpacePoint
	err := s.pool.QueryRow(ctx,
		`SELECT id, latitude, longitude FROM spaces WHERE id = $1`, spaceID).
		Scan(&p.ID, &p.Lat, &p.Lng)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpacePoint{}, ErrNotFound
	}
	if err != nil {
		return SpacePoint{}, fmt.Errorf("get space point: %w", err)
	}
	return p, nil
}

// Nearby returns active landmarks within radius, nearest first, as the crow
// flies.
//
// This is the prefilter that makes routing affordable. Routing a listing
// against every landmark in Ghana would be thousands of pointless pairs;
// narrowing to a handful with the GiST index first means one small Table call.
// The expression repeats migration 00018's index expression exactly, or the
// planner falls back to a sequential scan.
func (s *Store) Nearby(ctx context.Context, lat, lng float64, radiusM int, limit int) ([]Landmark, error) {
	const q = `
		SELECT id, name, kind::text, locality, latitude, longitude
		FROM landmarks
		WHERE active
		  AND ST_DWithin(
		        ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography,
		        ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography,
		        $3)
		ORDER BY ST_Distance(
		        ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography,
		        ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography)
		LIMIT $4`

	rows, err := s.pool.Query(ctx, q, lat, lng, radiusM, limit)
	if err != nil {
		return nil, fmt.Errorf("find nearby landmarks: %w", err)
	}
	defer rows.Close()

	out := []Landmark{}
	for rows.Next() {
		var l Landmark
		if err := rows.Scan(&l.ID, &l.Name, &l.Kind, &l.Locality, &l.Lat, &l.Lng); err != nil {
			return nil, fmt.Errorf("scan landmark: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Replace writes a listing's walking times, swapping the whole set in one
// transaction. A partial update would leave a listing quoting the old walk to
// one landmark and the new walk to another.
func (s *Store) Replace(ctx context.Context, spaceID uuid.UUID, walks []Walk) error {
	return postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM space_landmarks WHERE space_id = $1`, spaceID); err != nil {
			return fmt.Errorf("clear walks: %w", err)
		}
		if len(walks) == 0 {
			return nil
		}

		rows := make([][]any, 0, len(walks))
		for _, w := range walks {
			rows = append(rows, []any{spaceID, w.Landmark.ID, w.Seconds, w.Metres})
		}

		_, err := tx.CopyFrom(ctx,
			pgx.Identifier{"space_landmarks"},
			[]string{"space_id", "landmark_id", "walk_seconds", "walk_metres"},
			pgx.CopyFromRows(rows))
		if err != nil {
			return fmt.Errorf("insert walks: %w", err)
		}
		return nil
	})
}

// ForSpace returns a listing's walking times, nearest first. The listing page
// shows the top few; search shows one.
func (s *Store) ForSpace(ctx context.Context, spaceID uuid.UUID, limit int) ([]Walk, error) {
	const q = `
		SELECT l.id, l.name, l.kind::text, l.locality, l.latitude, l.longitude,
		       sl.walk_seconds, sl.walk_metres
		FROM space_landmarks sl
		JOIN landmarks l ON l.id = sl.landmark_id
		WHERE sl.space_id = $1 AND l.active
		ORDER BY sl.walk_seconds
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, spaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list walks: %w", err)
	}
	defer rows.Close()

	out := []Walk{}
	for rows.Next() {
		var w Walk
		if err := rows.Scan(&w.Landmark.ID, &w.Landmark.Name, &w.Landmark.Kind,
			&w.Landmark.Locality, &w.Landmark.Lat, &w.Landmark.Lng,
			&w.Seconds, &w.Metres); err != nil {
			return nil, fmt.Errorf("scan walk: %w", err)
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Unrouted returns published listings with no walking times yet, oldest first.
// New listings get routed by a job enqueued at publish; this is the backfill
// that catches the ones published while OSRM was down.
func (s *Store) Unrouted(ctx context.Context, limit int32) ([]SpacePoint, error) {
	const q = `
		SELECT s.id, s.latitude, s.longitude
		FROM spaces s
		WHERE s.status = 'published'
		  AND NOT EXISTS (SELECT 1 FROM space_landmarks sl WHERE sl.space_id = s.id)
		ORDER BY s.published_at
		LIMIT $1`

	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list unrouted spaces: %w", err)
	}
	defer rows.Close()

	out := []SpacePoint{}
	for rows.Next() {
		var p SpacePoint
		if err := rows.Scan(&p.ID, &p.Lat, &p.Lng); err != nil {
			return nil, fmt.Errorf("scan space point: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Stale returns listings whose walks were computed before a cutoff, so a
// corrected landmark coordinate can be rolled out across existing listings.
func (s *Store) Stale(ctx context.Context, before time.Time, limit int32) ([]SpacePoint, error) {
	const q = `
		SELECT DISTINCT s.id, s.latitude, s.longitude
		FROM spaces s
		JOIN space_landmarks sl ON sl.space_id = s.id
		WHERE s.status = 'published' AND sl.computed_at < $1
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list stale spaces: %w", err)
	}
	defer rows.Close()

	out := []SpacePoint{}
	for rows.Next() {
		var p SpacePoint
		if err := rows.Scan(&p.ID, &p.Lat, &p.Lng); err != nil {
			return nil, fmt.Errorf("scan space point: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
