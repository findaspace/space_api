package booking

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Space is what the booking engine needs to know about a listing. Declared
// here, so this package never imports listing.
type Space struct {
	ID           uuid.UUID
	HostID       uuid.UUID
	Mode         Mode
	Status       string
	Currency     string
	MaxOccupancy int
}

type SpaceSource interface {
	BookableSpace(ctx context.Context, spaceID uuid.UUID) (Space, error)
}

type PriceRequest struct {
	CheckIn  time.Time
	CheckOut time.Time
	StartsOn time.Time
	Months   int
	Guests   int
}

// Priced is a server-computed quote. The client never supplies any of it.
type Priced struct {
	Currency      string
	StartsOn      time.Time
	EndsOn        time.Time
	Nights        int
	Months        int
	SubtotalMinor int64
	DiscountMinor int64
	FeesMinor     int64
	TotalMinor    int64
	DueNowMinor   int64
	Policy        Policy
	Raw           any
}

type Pricer interface {
	Price(ctx context.Context, sp Space, req PriceRequest) (Priced, error)
}

type ServiceConfig struct {
	HoldTTL time.Duration
}

type Service struct {
	store  *Store
	spaces SpaceSource
	pricer Pricer
	cfg    ServiceConfig
	now    func() time.Time
}

func NewService(store *Store, spaces SpaceSource, pricer Pricer, cfg ServiceConfig) *Service {
	return &Service{store: store, spaces: spaces, pricer: pricer, cfg: cfg, now: time.Now}
}

type HoldRequest struct {
	SpaceID        uuid.UUID
	GuestID        uuid.UUID
	CheckIn        time.Time
	CheckOut       time.Time
	StartsOn       time.Time
	Months         int
	Guests         int
	IdempotencyKey string
}

func (s *Service) Hold(ctx context.Context, req HoldRequest) (Booking, error) {
	sp, err := s.spaces.BookableSpace(ctx, req.SpaceID)
	if err != nil {
		return Booking{}, err
	}
	if sp.HostID == req.GuestID {
		return Booking{}, ErrOwnListing
	}

	// Priced server-side, every time. The number the client displayed is a
	// hint; trusting it lets a crafted request book an apartment for one pesewa.
	priced, err := s.pricer.Price(ctx, sp, PriceRequest{
		CheckIn: req.CheckIn, CheckOut: req.CheckOut,
		StartsOn: req.StartsOn, Months: req.Months, Guests: req.Guests,
	})
	if err != nil {
		return Booking{}, err
	}

	guests := req.Guests
	if guests < 1 {
		guests = 1
	}

	return s.store.Hold(ctx, HoldInput{
		SpaceID: sp.ID, HostID: sp.HostID, GuestID: req.GuestID,
		Mode:     sp.Mode,
		StartsOn: priced.StartsOn, EndsOn: priced.EndsOn,
		Nights: priced.Nights, Months: priced.Months, Guests: guests,
		Currency:      priced.Currency,
		SubtotalMinor: priced.SubtotalMinor,
		DiscountMinor: priced.DiscountMinor,
		FeesMinor:     priced.FeesMinor,
		TotalMinor:    priced.TotalMinor,
		DueNowMinor:   priced.DueNowMinor,
		Quote:         priced.Raw,
		Policy:        priced.Policy,

		IdempotencyKey: req.IdempotencyKey,
		HoldFor:        s.cfg.HoldTTL,
	})
}

func (s *Service) Get(ctx context.Context, id, userID uuid.UUID) (Booking, error) {
	return s.store.ByIDForParty(ctx, id, userID)
}

func (s *Service) ListAsGuest(ctx context.Context, guestID uuid.UUID, limit, offset int32) ([]Booking, error) {
	return s.store.ListForGuest(ctx, guestID, limit, offset)
}

func (s *Service) ListAsHost(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Booking, error) {
	return s.store.ListForHost(ctx, hostID, limit, offset)
}

// actorFor derives the role from the booking itself. Taking it from the request
// would let a guest claim to be the host and cancel with a full refund.
func actorFor(b Booking, userID uuid.UUID, isAdmin bool) (Actor, error) {
	switch {
	case isAdmin:
		return ActorAdmin, nil
	case b.GuestID == userID:
		return ActorGuest, nil
	case b.HostID == userID:
		return ActorHost, nil
	default:
		return "", ErrNotFound
	}
}

type CancelResult struct {
	Booking Booking
	Refund  RefundOutcome
}

func (s *Service) Cancel(ctx context.Context, id, userID uuid.UUID, isAdmin bool, reason string) (CancelResult, error) {
	b, err := s.store.ByIDForParty(ctx, id, userID)
	if err != nil {
		return CancelResult{}, err
	}

	actor, err := actorFor(b, userID, isAdmin)
	if err != nil {
		return CancelResult{}, err
	}
	if err := Authorize(b.Mode, b.Status, StatusCancelled, actor); err != nil {
		return CancelResult{}, err
	}

	refund := ComputeRefund(RefundInput{
		Policy: b.Policy, Mode: b.Mode, Status: b.Status, Actor: actor,
		DueNowMinor: b.DueNowMinor, FeesMinor: b.FeesMinor,
		StartsOn: b.StartsOn, Now: s.now(),
	})

	// The refund is recorded here and paid out in Phase 6. Writing the amount
	// at decision time means the policy that applied is the one in force when
	// the guest cancelled, not whenever the payout job happens to run.
	out, err := s.store.Transition(ctx, TransitionInput{
		ID: b.ID, From: b.Status, To: StatusCancelled,
		Actor: actor, Reason: reason, Refund: refund.RefundMinor,
	})
	if err != nil {
		return CancelResult{}, err
	}
	return CancelResult{Booking: out, Refund: refund}, nil
}

// Quote returns what a cancellation would refund, without cancelling. Guests
// deserve to see the number before they commit to it.
func (s *Service) CancellationQuote(ctx context.Context, id, userID uuid.UUID, isAdmin bool) (RefundOutcome, error) {
	b, err := s.store.ByIDForParty(ctx, id, userID)
	if err != nil {
		return RefundOutcome{}, err
	}
	actor, err := actorFor(b, userID, isAdmin)
	if err != nil {
		return RefundOutcome{}, err
	}
	return ComputeRefund(RefundInput{
		Policy: b.Policy, Mode: b.Mode, Status: b.Status, Actor: actor,
		DueNowMinor: b.DueNowMinor, FeesMinor: b.FeesMinor,
		StartsOn: b.StartsOn, Now: s.now(),
	}), nil
}

func (s *Service) Approve(ctx context.Context, id, hostID uuid.UUID) (Booking, error) {
	return s.hostDecision(ctx, id, hostID, StatusApproved, "")
}

func (s *Service) Decline(ctx context.Context, id, hostID uuid.UUID, reason string) (Booking, error) {
	return s.hostDecision(ctx, id, hostID, StatusDeclined, reason)
}

func (s *Service) hostDecision(ctx context.Context, id, hostID uuid.UUID, to Status, reason string) (Booking, error) {
	b, err := s.store.ByIDForParty(ctx, id, hostID)
	if err != nil {
		return Booking{}, err
	}
	if b.HostID != hostID {
		return Booking{}, ErrNotPermitted
	}
	if err := Authorize(b.Mode, b.Status, to, ActorHost); err != nil {
		return Booking{}, err
	}
	if b.HoldExpiresAt != nil && s.now().After(*b.HoldExpiresAt) {
		return Booking{}, ErrHoldExpired
	}

	return s.store.Transition(ctx, TransitionInput{
		ID: b.ID, From: b.Status, To: to, Actor: ActorHost, Reason: reason,
	})
}

func (s *Service) ByID(ctx context.Context, id uuid.UUID) (Booking, error) {
	return s.store.ByID(ctx, id)
}

func (s *Service) MarkPaidTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, from Status) error {
	b, err := s.store.ByID(ctx, id)
	if err != nil {
		return err
	}
	if err := Authorize(b.Mode, from, StatusConfirmed, ActorSystem); err != nil {
		return err
	}
	return s.store.TransitionTx(ctx, tx, TransitionInput{
		ID: id, From: from, To: StatusConfirmed, Actor: ActorSystem,
	})
}
