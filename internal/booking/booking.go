package booking

import (
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusHeld      Status = "held"
	StatusApproved  Status = "approved"
	StatusConfirmed Status = "confirmed"
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusCancelled Status = "cancelled"
	StatusDeclined  Status = "declined"
	StatusExpired   Status = "expired"
)

type Actor string

const (
	ActorGuest  Actor = "guest"
	ActorHost   Actor = "host"
	ActorSystem Actor = "system"
	ActorAdmin  Actor = "admin"
)

type Mode string

const (
	ModeNightly Mode = "nightly"
	ModeTerm    Mode = "term"
)

type Booking struct {
	ID        uuid.UUID
	Reference string
	SpaceID   uuid.UUID
	HostID    uuid.UUID
	GuestID   uuid.UUID

	Mode   Mode
	Status Status

	StartsOn time.Time
	EndsOn   time.Time
	Nights   int
	Months   int
	Guests   int
	Policy   Policy

	Currency      string
	SubtotalMinor int64
	DiscountMinor int64
	FeesMinor     int64
	TotalMinor    int64
	DueNowMinor   int64
	Quote         map[string]any

	HoldExpiresAt *time.Time
	ApprovedAt    *time.Time
	ConfirmedAt   *time.Time
	CancelledAt   *time.Time
	CancelledBy   *Actor
	CancelReason  string
	RefundMinor   int64

	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	ErrNotFound      = errors.New("booking: not found")
	ErrDatesTaken    = errors.New("booking: those dates are already booked")
	ErrOwnListing    = errors.New("booking: you cannot book your own listing")
	ErrBadTransition = errors.New("booking: status change not allowed")
	ErrNotPermitted  = errors.New("booking: not allowed for this actor")
	ErrHoldExpired   = errors.New("booking: the hold has expired")
	ErrConflict      = errors.New("booking: booking changed concurrently")
)

// Crockford base32: no I, L, O or U, so a reference read out over the phone
// cannot be misheard as a digit. Guests will read these to hosts constantly.
const refAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func NewReference() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate reference: %w", err)
	}

	n := uint64(b[0])<<32 | uint64(b[1])<<24 | uint64(b[2])<<16 | uint64(b[3])<<8 | uint64(b[4])
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = refAlphabet[n&31]
		n >>= 5
	}
	return "SPC-" + string(out), nil
}

// ByID skips the party check. Only the payment path calls it, driven by a
// verified webhook rather than by a user.
