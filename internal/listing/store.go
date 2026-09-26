package listing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres/dbgen"
)

type Store struct {
	pool *pgxpool.Pool
	q    *dbgen.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: dbgen.New(pool)}
}

type Summary struct {
	Slug           string
	Title          string
	Latitude       float64
	Longitude      float64
	Locality       string
	Landmark       string
	DigitalAddress string
}

func (s *Store) Summaries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Summary, error) {
	out := make(map[uuid.UUID]Summary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q.SpaceSummaries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("space summaries: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = Summary{
			Slug: r.Slug, Title: r.Title, Latitude: r.Latitude, Longitude: r.Longitude,
			Locality: r.Locality, Landmark: r.Landmark, DigitalAddress: r.DigitalAddress,
		}
	}
	return out, nil
}

// toDomain converts the generated row into the type the rest of the codebase
// uses. Keeping dbgen out of the service layer means a schema change is a
// compile error here and nowhere else.
func toDomain(row dbgen.Space) (Space, error) {
	attrs := map[string]any{}
	if len(row.Attributes) > 0 {
		if err := json.Unmarshal(row.Attributes, &attrs); err != nil {
			return Space{}, fmt.Errorf("decode attributes for %s: %w", row.ID, err)
		}
	}

	return Space{
		ID:             row.ID,
		HostID:         row.HostID,
		Slug:           row.Slug,
		Type:           SpaceType(row.SpaceType),
		Mode:           RentalMode(row.RentalMode),
		Status:         Status(row.Status),
		Title:          row.Title,
		Description:    row.Description,
		BasePriceMinor: row.BasePriceMinor,
		Currency:       row.Currency,
		PricePeriod:    PricePeriod(row.PricePeriod),
		DepositMinor:   row.DepositMinor,
		AdvanceMonths:  row.AdvanceMonths,
		MaxOccupancy:   row.MaxOccupancy,
		Location: Location{
			Latitude:       row.Latitude,
			Longitude:      row.Longitude,
			Locality:       row.Locality,
			Region:         row.Region,
			Country:        row.Country,
			Landmark:       row.Landmark,
			DigitalAddress: row.DigitalAddress,
		},
		Amenities:   row.Amenities,
		Attributes:  attrs,
		PublishedAt: timestamptzPtr(row.PublishedAt),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}

func timestamptzPtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	return &ts.Time
}

func (s *Store) Create(ctx context.Context, in Space) (Space, error) {
	attrs, err := json.Marshal(in.Attributes)
	if err != nil {
		return Space{}, fmt.Errorf("encode attributes: %w", err)
	}

	row, err := s.q.CreateSpace(ctx, dbgen.CreateSpaceParams{
		HostID:         in.HostID,
		Slug:           in.Slug,
		SpaceType:      dbgen.SpaceType(in.Type),
		RentalMode:     dbgen.RentalMode(in.Mode),
		Title:          in.Title,
		Description:    in.Description,
		BasePriceMinor: in.BasePriceMinor,
		Currency:       in.Currency,
		PricePeriod:    dbgen.PricePeriod(in.PricePeriod),
		DepositMinor:   in.DepositMinor,
		AdvanceMonths:  in.AdvanceMonths,
		MaxOccupancy:   in.MaxOccupancy,
		Latitude:       in.Location.Latitude,
		Longitude:      in.Location.Longitude,
		Locality:       in.Location.Locality,
		Region:         in.Location.Region,
		Country:        in.Location.Country,
		Landmark:       in.Location.Landmark,
		DigitalAddress: in.Location.DigitalAddress,
		Amenities:      in.Amenities,
		Attributes:     attrs,
	})
	if err != nil {
		return Space{}, fmt.Errorf("create space: %w", err)
	}
	return toDomain(row)
}

func (s *Store) GetForHost(ctx context.Context, id, hostID uuid.UUID) (Space, error) {
	row, err := s.q.GetSpaceForHost(ctx, dbgen.GetSpaceForHostParams{ID: id, HostID: hostID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("get space: %w", err)
	}
	return toDomain(row)
}

func (s *Store) GetPublishedBySlug(ctx context.Context, slug string) (Space, error) {
	row, err := s.q.GetPublishedSpaceBySlug(ctx, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("get space by slug: %w", err)
	}
	return toDomain(row)
}

func (s *Store) ListForHost(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Space, error) {
	rows, err := s.q.ListSpacesForHost(ctx, dbgen.ListSpacesForHostParams{
		HostID:     hostID,
		PageLimit:  limit,
		PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list spaces: %w", err)
	}

	out := make([]Space, 0, len(rows))
	for _, row := range rows {
		sp, err := toDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, nil
}

type Patch struct {
	Title          *string
	Description    *string
	BasePriceMinor *int64
	DepositMinor   *int64
	AdvanceMonths  *int16
	MaxOccupancy   *int16
	Latitude       *float64
	Longitude      *float64
	Locality       *string
	Region         *string
	Landmark       *string
	DigitalAddress *string
	Amenities      []string
	Attributes     map[string]any
}

func (s *Store) Update(ctx context.Context, id, hostID uuid.UUID, p Patch) (Space, error) {
	var attrs []byte
	if p.Attributes != nil {
		var err error
		if attrs, err = json.Marshal(p.Attributes); err != nil {
			return Space{}, fmt.Errorf("encode attributes: %w", err)
		}
	}

	row, err := s.q.UpdateSpace(ctx, dbgen.UpdateSpaceParams{
		ID:             id,
		HostID:         hostID,
		Title:          p.Title,
		Description:    p.Description,
		BasePriceMinor: p.BasePriceMinor,
		DepositMinor:   p.DepositMinor,
		AdvanceMonths:  p.AdvanceMonths,
		MaxOccupancy:   p.MaxOccupancy,
		Latitude:       p.Latitude,
		Longitude:      p.Longitude,
		Locality:       p.Locality,
		Region:         p.Region,
		Landmark:       p.Landmark,
		DigitalAddress: p.DigitalAddress,
		Amenities:      p.Amenities,
		Attributes:     attrs,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Missing, not yours, or published. All three are 404 to the caller.
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("update space: %w", err)
	}
	return toDomain(row)
}

// SetStatus applies a transition only if the row is still in the status the
// caller read. Zero rows means somebody else moved it first.
func (s *Store) SetStatus(ctx context.Context, id uuid.UUID, from, to Status) (Space, error) {
	row, err := s.q.SetSpaceStatus(ctx, dbgen.SetSpaceStatusParams{
		ID:             id,
		NextStatus:     dbgen.SpaceStatus(to),
		ExpectedStatus: dbgen.SpaceStatus(from),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Space{}, ErrConflict
	}
	if err != nil {
		return Space{}, fmt.Errorf("set status: %w", err)
	}
	return toDomain(row)
}

func (s *Store) GetAny(ctx context.Context, id uuid.UUID) (Space, error) {
	row, err := s.q.GetSpaceByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Space{}, ErrNotFound
	}
	if err != nil {
		return Space{}, fmt.Errorf("get space: %w", err)
	}
	return toDomain(row)
}
