package payout

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Channel string

const (
	ChannelMobileMoney Channel = "mobile_money"
	ChannelBank        Channel = "bank"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusPaid       Status = "paid"
	StatusFailed     Status = "failed"
	StatusReversed   Status = "reversed"
)

// Paystack's Ghana mobile money codes. Vodafone Ghana is now Telecel, but the
// code is still VOD; changing it here would break every existing recipient.
const (
	NetworkMTN        = "MTN"
	NetworkTelecel    = "VOD"
	NetworkAirtelTigo = "ATL"
)

// Paystack refuses transfers outside this range, so refuse them here rather
// than discovering it from a 400 halfway through a payout run.
const (
	MinPayoutMinor int64 = 100        // GHS 1
	MaxPayoutMinor int64 = 10_000_000 // GHS 100,000
)

type Account struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	RecipientCode string
	Channel       Channel
	BankCode      string
	AccountNumber string
	AccountName   string
	Currency      string
	VerifiedAt    *time.Time
	IsDefault     bool
	CreatedAt     time.Time
}

type Payout struct {
	ID            uuid.UUID
	HostID        uuid.UUID
	AccountID     uuid.UUID
	Reference     string
	TransferCode  string
	Status        Status
	Currency      string
	AmountMinor   int64
	FailureReason string
	SettledAt     *time.Time
	CreatedAt     time.Time
}

type Balance struct {
	Currency          string `json:"currency"`
	TotalMinor        int64  `json:"total_minor"`
	AvailableMinor    int64  `json:"available_minor"`
	PendingMinor      int64  `json:"pending_minor"`
	InFlightMinor     int64  `json:"in_flight_minor"`
	WithdrawableMinor int64  `json:"withdrawable_minor"`
}

var (
	ErrNoAccount       = errors.New("payout: no payout account on file")
	ErrNotFound        = errors.New("payout: not found")
	ErrInProgress      = errors.New("payout: a payout is already in progress")
	ErrBelowMinimum    = errors.New("payout: amount is below the minimum")
	ErrAboveMaximum    = errors.New("payout: amount is above the maximum")
	ErrInsufficient    = errors.New("payout: amount exceeds the available balance")
	ErrUnknownNetwork  = errors.New("payout: unrecognised mobile money network")
	ErrAccountMismatch = errors.New("payout: account does not belong to this host")
)

// ghanaPrefixes maps the leading digits of a national number to the network
// that originally issued it.
var ghanaPrefixes = map[string]string{
	"24": NetworkMTN, "25": NetworkMTN, "53": NetworkMTN,
	"54": NetworkMTN, "55": NetworkMTN, "59": NetworkMTN,

	"20": NetworkTelecel, "50": NetworkTelecel,

	"26": NetworkAirtelTigo, "27": NetworkAirtelTigo,
	"56": NetworkAirtelTigo, "57": NetworkAirtelTigo,
}

// GuessNetwork suggests a network from an E.164 Ghanaian number. It is a
// default for the signup form, never a decision: number portability means a
// 024 number can sit on AirtelTigo. The host confirms, and Paystack's
// recipient creation is what actually validates the pairing.
func GuessNetwork(e164 string) (string, bool) {
	national, ok := strings.CutPrefix(e164, "+233")
	if !ok || len(national) < 3 {
		return "", false
	}
	network, ok := ghanaPrefixes[national[:2]]
	return network, ok
}

func ValidNetwork(code string) bool {
	switch code {
	case NetworkMTN, NetworkTelecel, NetworkAirtelTigo:
		return true
	}
	return false
}

// ValidateAmount checks a requested withdrawal against the provider's limits
// and the host's own available balance.
func ValidateAmount(amountMinor, availableMinor int64) error {
	switch {
	case amountMinor < MinPayoutMinor:
		return ErrBelowMinimum
	case amountMinor > MaxPayoutMinor:
		return ErrAboveMaximum
	case amountMinor > availableMinor:
		return ErrInsufficient
	}
	return nil
}

// Releasable reports whether a booking's earnings have matured. Money is held
// until after the guest has arrived, so a no-show can still be refunded from
// funds we hold rather than chased from a host who has already withdrawn.
func Releasable(startsOn time.Time, holdDays int, now time.Time) bool {
	day := func(t time.Time) time.Time {
		y, m, d := t.UTC().Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return !day(now).Before(day(startsOn).AddDate(0, 0, holdDays))
}

// NewReference returns the transfer reference. Paystack treats a repeat of the
// same reference as the same transfer, which is what makes a retry safe rather
// than a second credit.
func NewReference(payoutID uuid.UUID) string {
	return "pot-" + strings.ReplaceAll(payoutID.String(), "-", "")
}
