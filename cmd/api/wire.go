package main

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/payment"
)

// The composition root is where cross-domain adapters belong. booking declares
// what it needs, listing and calendar provide it, and neither imports the other.
type spaceSource struct{ listings *listing.Service }

type bookingGateway struct{ bookings *booking.Service }

func (g bookingGateway) Snapshot(ctx context.Context, id uuid.UUID) (payment.Snapshot, error) {
	b, err := g.bookings.ByID(ctx, id)
	if err != nil {
		return payment.Snapshot{}, err
	}
	return payment.Snapshot{
		ID: b.ID, Reference: b.Reference, HostID: b.HostID, GuestID: b.GuestID,
		Status: string(b.Status), Mode: string(b.Mode),
		Currency: b.Currency, DueNowMinor: b.DueNowMinor, FeesMinor: b.FeesMinor,
	}, nil
}

func (g bookingGateway) MarkPaid(ctx context.Context, tx pgx.Tx, id uuid.UUID, from string) error {
	return g.bookings.MarkPaidTx(ctx, tx, id, booking.Status(from))
}

type payerSource struct{ identity *identity.Service }

func (p payerSource) ContactFor(ctx context.Context, userID uuid.UUID) (string, string, error) {
	u, err := p.identity.Me(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if u.Email != nil {
		return *u.Email, u.Phone, nil
	}
	return "", u.Phone, nil
}

func (s spaceSource) BookableSpace(ctx context.Context, spaceID uuid.UUID) (booking.Space, error) {
	sp, err := s.listings.GetAnyPublished(ctx, spaceID)
	if err != nil {
		return booking.Space{}, err
	}
	return booking.Space{
		ID:           sp.ID,
		HostID:       sp.HostID,
		Mode:         booking.Mode(sp.Mode),
		Status:       string(sp.Status),
		Currency:     sp.Currency,
		MaxOccupancy: int(sp.MaxOccupancy),
	}, nil
}

type pricer struct {
	listings  *listing.Service
	calendars *calendar.Service
}

func (p pricer) Price(ctx context.Context, sp booking.Space, req booking.PriceRequest) (booking.Priced, error) {
	full, err := p.listings.GetAnyPublished(ctx, sp.ID)
	if err != nil {
		return booking.Priced{}, err
	}

	q, err := p.calendars.Quote(ctx, calendar.SpacePricing{
		SpaceID:        full.ID,
		Mode:           string(full.Mode),
		Status:         string(full.Status),
		Currency:       full.Currency,
		BasePriceMinor: full.BasePriceMinor,
		PricePeriod:    string(full.PricePeriod),
		DepositMinor:   full.DepositMinor,
		AdvanceMonths:  int(full.AdvanceMonths),
		MaxOccupancy:   int(full.MaxOccupancy),
	}, calendar.QuoteRequest{
		CheckIn: req.CheckIn, CheckOut: req.CheckOut,
		StartsOn: req.StartsOn, Months: req.Months, Guests: req.Guests,
	})
	if err != nil {
		return booking.Priced{}, err
	}

	out := booking.Priced{
		Currency:      q.Currency,
		Nights:        q.Nights,
		Months:        q.Months,
		SubtotalMinor: q.SubtotalMinor,
		DiscountMinor: q.DiscountMinor,
		FeesMinor:     q.FeesMinor,
		TotalMinor:    q.TotalMinor,
		DueNowMinor:   q.DueNowMinor,
		Policy:        booking.Policy(q.Policy),
		Raw:           q,
	}
	if q.CheckIn != nil {
		out.StartsOn = *q.CheckIn
	}
	if q.CheckOut != nil {
		out.EndsOn = *q.CheckOut
	}
	return out, nil
}

// GetAnyPublished fetches a listing by id without an ownership check. Only
// published listings are returned, so it is safe for the booking path.
// func (s *Service) GetAnyPublished(ctx context.Context, id uuid.UUID) (Space, error) {
// 	sp, err := s.store.GetAny(ctx, id)
// 	if err != nil {
// 		return Space{}, err
// 	}
// 	if sp.Status != StatusPublished {
// 		return Space{}, ErrNotFound
// 	}
// 	return sp, nil
// }
