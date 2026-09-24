package calendar

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const maxCalendarDays = 400

type ServiceConfig struct {
	Fees FeeConfig
}

type Service struct {
	store *Store
	cfg   ServiceConfig
	now   func() time.Time
}

func NewService(store *Store, cfg ServiceConfig) *Service {
	return &Service{store: store, cfg: cfg, now: time.Now}
}

type QuoteRequest struct {
	CheckIn  time.Time
	CheckOut time.Time
	Guests   int
	StartsOn time.Time
	Months   int
}

// Quote branches on the listing's rental mode. The caller supplies the pricing
// snapshot, so this package never reaches into listing.
func (s *Service) Quote(ctx context.Context, sp SpacePricing, req QuoteRequest) (Quote, error) {
	switch sp.Mode {
	case "nightly":
		return s.quoteNightly(ctx, sp, req)
	case "term":
		return s.quoteTerm(ctx, sp, req)
	default:
		return Quote{}, ErrModeMismatch
	}
}

func (s *Service) quoteNightly(ctx context.Context, sp SpacePricing, req QuoteRequest) (Quote, error) {
	checkIn, checkOut := Day(req.CheckIn), Day(req.CheckOut)
	if !checkOut.After(checkIn) {
		return Quote{}, ErrInvalidRange
	}
	if Nights(checkIn, checkOut) > MaxStayNights {
		return Quote{}, ErrTooLong
	}

	rules, err := s.store.Rules(ctx, sp.SpaceID)
	if err != nil {
		return Quote{}, err
	}
	plans, err := s.store.PlansInWindow(ctx, sp.SpaceID, checkIn, checkOut)
	if err != nil {
		return Quote{}, err
	}
	blocks, err := s.store.BlocksInWindow(ctx, sp.SpaceID, checkIn, checkOut)
	if err != nil {
		return Quote{}, err
	}

	guests := req.Guests
	if guests < 1 {
		guests = 1
	}

	return QuoteNightly(NightlyInput{
		Space: sp, CheckIn: checkIn, CheckOut: checkOut, Guests: guests,
		Rules: rules, Plans: plans, Blocks: blocks,
		Fees: s.cfg.Fees, Today: s.now(),
	})
}

func (s *Service) quoteTerm(ctx context.Context, sp SpacePricing, req QuoteRequest) (Quote, error) {
	start := Day(req.StartsOn)
	if req.Months < 1 {
		return Quote{}, ErrInvalidRange
	}

	end := start.AddDate(0, req.Months, 0)
	blocks, err := s.store.BlocksInWindow(ctx, sp.SpaceID, start, end)
	if err != nil {
		return Quote{}, err
	}

	rules, err := s.store.Rules(ctx, sp.SpaceID)
	if err != nil {
		return Quote{}, err
	}

	return QuoteTerm(TermInput{
		Space: sp, StartsOn: start, Months: req.Months, Blocks: blocks,
		Policy: rules.CancellationPolicy,
		Fees:   s.cfg.Fees, Today: s.now(),
	})
}

// Calendar returns per-night price and availability for a window. This is what
// paints the date picker, so it answers for unavailable nights too rather than
// omitting them.
func (s *Service) Calendar(ctx context.Context, sp SpacePricing, from, to time.Time) ([]NightPrice, error) {
	from, to = Day(from), Day(to)
	if !to.After(from) {
		return nil, ErrInvalidRange
	}
	if Nights(from, to) > maxCalendarDays {
		return nil, ErrHorizon
	}

	plans, err := s.store.PlansInWindow(ctx, sp.SpaceID, from, to)
	if err != nil {
		return nil, err
	}
	blocks, err := s.store.BlocksInWindow(ctx, sp.SpaceID, from, to)
	if err != nil {
		return nil, err
	}

	nights := ResolveNights(from, to, sp.BasePriceMinor, plans, blocks)

	// Past dates are never bookable, whatever the host's calendar says.
	today := Day(s.now())
	for i := range nights {
		if nights[i].Date.Before(today) {
			nights[i].Available = false
		}
	}
	return nights, nil
}

func (s *Service) Rules(ctx context.Context, spaceID uuid.UUID) (StayRules, error) {
	return s.store.Rules(ctx, spaceID)
}

func (s *Service) SetRules(ctx context.Context, spaceID uuid.UUID, r StayRules) (StayRules, error) {
	if r.MinNights < 1 {
		r.MinNights = 1
	}
	if r.MaxNights > 0 && r.MaxNights < r.MinNights {
		return StayRules{}, ErrInvalidRange
	}
	if r.CancellationPolicy == "" {
		r.CancellationPolicy = "moderate"
	}
	return s.store.SetRules(ctx, spaceID, r)
}

func (s *Service) ListPlans(ctx context.Context, spaceID uuid.UUID) ([]RatePlan, error) {
	return s.store.AllPlans(ctx, spaceID)
}

func (s *Service) CreatePlan(ctx context.Context, p RatePlan) (RatePlan, error) {
	p.StartsOn, p.EndsOn = Day(p.StartsOn), Day(p.EndsOn)
	if !p.EndsOn.After(p.StartsOn) {
		return RatePlan{}, ErrInvalidRange
	}
	if p.PriceMinor <= 0 {
		return RatePlan{}, ErrInvalidPrice
	}
	return s.store.CreatePlan(ctx, p)
}

func (s *Service) DeletePlan(ctx context.Context, id, spaceID uuid.UUID) error {
	return s.store.DeletePlan(ctx, id, spaceID)
}

func (s *Service) ListBlocks(ctx context.Context, spaceID uuid.UUID, from, to time.Time) ([]Block, error) {
	return s.store.BlocksInWindow(ctx, spaceID, Day(from), Day(to))
}

func (s *Service) CreateBlock(ctx context.Context, b Block) (Block, error) {
	b.StartsOn, b.EndsOn = Day(b.StartsOn), Day(b.EndsOn)
	if !b.EndsOn.After(b.StartsOn) {
		return Block{}, ErrInvalidRange
	}
	return s.store.CreateBlock(ctx, b)
}

func (s *Service) DeleteBlock(ctx context.Context, id, spaceID uuid.UUID) error {
	return s.store.DeleteBlock(ctx, id, spaceID)
}
