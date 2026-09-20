package calendar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres/dbgen"
)

var (
	ErrOverlap      = errors.New("calendar: rate plan overlaps an existing one")
	ErrNotFound     = errors.New("calendar: not found")
	ErrInvalidPrice = errors.New("calendar: price must be positive")
)

type Store struct{ q *dbgen.Queries }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: dbgen.New(pool)} }

// mapPgError translates constraint violations by name. Matching on SQLSTATE
// alone would collapse every CHECK on the table into one message.
func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	switch pgErr.ConstraintName {
	case "rate_plans_no_overlap":
		return ErrOverlap
	case "rate_plans_period", "availability_blocks_period":
		return ErrInvalidRange
	case "rate_plans_price":
		return ErrInvalidPrice
	}
	return err
}

func toInt(v *int16) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func toInt16(v *int) *int16 {
	if v == nil {
		return nil
	}
	n := int16(*v)
	return &n
}

func planToDomain(row dbgen.RatePlan) RatePlan {
	return RatePlan{
		ID:         row.ID,
		SpaceID:    row.SpaceID,
		Name:       row.Name,
		StartsOn:   Day(row.StartsOn),
		EndsOn:     Day(row.EndsOn),
		PriceMinor: row.PriceMinor,
		MinNights:  toInt(row.MinNights),
	}
}

func blockToDomain(row dbgen.AvailabilityBlock) Block {
	return Block{
		ID:       row.ID,
		SpaceID:  row.SpaceID,
		StartsOn: Day(row.StartsOn),
		EndsOn:   Day(row.EndsOn),
		Reason:   row.Reason,
	}
}

// Rules returns the space's stay rules, or the defaults when the host has never
// set any. A missing row is the normal case, not an error.
func (s *Store) Rules(ctx context.Context, spaceID uuid.UUID) (StayRules, error) {
	row, err := s.q.GetStayRules(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultStayRules(), nil
	}
	if err != nil {
		return StayRules{}, fmt.Errorf("get stay rules: %w", err)
	}

	rules := StayRules{
		MinNights:          int(row.MinNights),
		WeeklyDiscountBps:  int(row.WeeklyDiscountBps),
		MonthlyDiscountBps: int(row.MonthlyDiscountBps),
		CleaningFeeMinor:   row.CleaningFeeMinor,
		CancellationPolicy: string(row.CancellationPolicy),
	}
	if row.MaxNights != nil {
		rules.MaxNights = int(*row.MaxNights)
	}
	return rules, nil
}

func (s *Store) SetRules(ctx context.Context, spaceID uuid.UUID, r StayRules) (StayRules, error) {
	var maxNights *int16
	if r.MaxNights > 0 {
		n := int16(r.MaxNights)
		maxNights = &n
	}

	row, err := s.q.UpsertStayRules(ctx, dbgen.UpsertStayRulesParams{
		SpaceID:            spaceID,
		MinNights:          int16(r.MinNights),
		MaxNights:          maxNights,
		WeeklyDiscountBps:  int16(r.WeeklyDiscountBps),
		MonthlyDiscountBps: int16(r.MonthlyDiscountBps),
		CleaningFeeMinor:   r.CleaningFeeMinor,
		CancellationPolicy: dbgen.CancellationPolicy(r.CancellationPolicy),
	})
	if err != nil {
		return StayRules{}, fmt.Errorf("upsert stay rules: %w", mapPgError(err))
	}

	out := StayRules{
		MinNights:          int(row.MinNights),
		WeeklyDiscountBps:  int(row.WeeklyDiscountBps),
		MonthlyDiscountBps: int(row.MonthlyDiscountBps),
		CleaningFeeMinor:   row.CleaningFeeMinor,
		CancellationPolicy: string(row.CancellationPolicy),
	}
	if row.MaxNights != nil {
		out.MaxNights = int(*row.MaxNights)
	}
	return out, nil
}

func (s *Store) CreatePlan(ctx context.Context, p RatePlan) (RatePlan, error) {
	row, err := s.q.CreateRatePlan(ctx, dbgen.CreateRatePlanParams{
		SpaceID:    p.SpaceID,
		Name:       p.Name,
		StartsOn:   p.StartsOn,
		EndsOn:     p.EndsOn,
		PriceMinor: p.PriceMinor,
		MinNights:  toInt16(p.MinNights),
	})
	if err != nil {
		return RatePlan{}, mapPgError(err)
	}
	return planToDomain(row), nil
}

func (s *Store) PlansInWindow(ctx context.Context, spaceID uuid.UUID, from, to time.Time) ([]RatePlan, error) {
	rows, err := s.q.ListRatePlansInWindow(ctx, dbgen.ListRatePlansInWindowParams{
		SpaceID: spaceID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return nil, fmt.Errorf("list rate plans: %w", err)
	}
	out := make([]RatePlan, 0, len(rows))
	for _, row := range rows {
		out = append(out, planToDomain(row))
	}
	return out, nil
}

func (s *Store) AllPlans(ctx context.Context, spaceID uuid.UUID) ([]RatePlan, error) {
	rows, err := s.q.ListRatePlans(ctx, spaceID)
	if err != nil {
		return nil, fmt.Errorf("list rate plans: %w", err)
	}
	out := make([]RatePlan, 0, len(rows))
	for _, row := range rows {
		out = append(out, planToDomain(row))
	}
	return out, nil
}

func (s *Store) DeletePlan(ctx context.Context, id, spaceID uuid.UUID) error {
	n, err := s.q.DeleteRatePlan(ctx, dbgen.DeleteRatePlanParams{ID: id, SpaceID: spaceID})
	if err != nil {
		return fmt.Errorf("delete rate plan: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateBlock(ctx context.Context, b Block) (Block, error) {
	row, err := s.q.CreateBlock(ctx, dbgen.CreateBlockParams{
		SpaceID:  b.SpaceID,
		StartsOn: b.StartsOn,
		EndsOn:   b.EndsOn,
		Reason:   b.Reason,
	})
	if err != nil {
		return Block{}, mapPgError(err)
	}
	return blockToDomain(row), nil
}

func (s *Store) BlocksInWindow(ctx context.Context, spaceID uuid.UUID, from, to time.Time) ([]Block, error) {
	rows, err := s.q.ListBlocksInWindow(ctx, dbgen.ListBlocksInWindowParams{
		SpaceID: spaceID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return nil, fmt.Errorf("list blocks: %w", err)
	}
	out := make([]Block, 0, len(rows))
	for _, row := range rows {
		out = append(out, blockToDomain(row))
	}
	return out, nil
}

func (s *Store) DeleteBlock(ctx context.Context, id, spaceID uuid.UUID) error {
	n, err := s.q.DeleteBlock(ctx, dbgen.DeleteBlockParams{ID: id, SpaceID: spaceID})
	if err != nil {
		return fmt.Errorf("delete block: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
