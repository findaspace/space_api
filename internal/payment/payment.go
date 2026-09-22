package payment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusAbandoned  Status = "abandoned"
)

type Payment struct {
	ID               uuid.UUID
	BookingID        uuid.UUID
	PayerID          uuid.UUID
	ProviderRef      string
	AccessCode       string
	AuthorizationURL string
	Channel          string
	Status           Status
	Currency         string
	AmountMinor      int64
	PaidAt           *time.Time
	CreatedAt        time.Time
}

var (
	ErrNotFound       = errors.New("payment: not found")
	ErrAlreadyPaid    = errors.New("payment: booking is already paid")
	ErrNotPayable     = errors.New("payment: booking is not awaiting payment")
	ErrAmountMismatch = errors.New("payment: amount does not match the booking")
	ErrBadSignature   = errors.New("payment: webhook signature is invalid")
	ErrDuplicate      = errors.New("payment: event already processed")
)

// Snapshot is what payments need to know about a booking. Declared here so this
// package never imports booking.
type Snapshot struct {
	ID          uuid.UUID
	Reference   string
	HostID      uuid.UUID
	GuestID     uuid.UUID
	Status      string
	Mode        string
	Currency    string
	DueNowMinor int64
	FeesMinor   int64
	StartsOn    time.Time
}

// BookingGateway takes a transaction on MarkPaid because confirming the booking
// and posting its ledger charge must commit together. This is the seam that
// would become a saga if bookings and payments ever split into two services.
type BookingGateway interface {
	Snapshot(ctx context.Context, bookingID uuid.UUID) (Snapshot, error)
	MarkPaid(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, from string) error
}

// Payable reports whether a booking is in a state where money should move.
// Nightly bookings pay from held; term bookings only after the host approves.
func Payable(mode, status string) bool {
	if mode == "term" {
		return status == "approved"
	}
	return status == "held"
}
