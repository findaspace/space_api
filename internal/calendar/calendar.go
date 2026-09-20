package calendar

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	// MaxHorizonDays caps how far ahead a stay can be quoted. Without it a
	// client can request a hundred-year range and make the engine allocate a
	// night price per day.
	MaxHorizonDays = 730
	MaxStayNights  = 365

	WeeklyThreshold  = 7
	MonthlyThreshold = 28

	BasisPoints = 10000
)

var (
	ErrInvalidRange  = errors.New("calendar: check-out must be after check-in")
	ErrPastDate      = errors.New("calendar: check-in is in the past")
	ErrHorizon       = errors.New("calendar: dates are too far ahead")
	ErrTooShort      = errors.New("calendar: stay is below the minimum nights")
	ErrTooLong       = errors.New("calendar: stay exceeds the maximum nights")
	ErrUnavailable   = errors.New("calendar: one or more nights are unavailable")
	ErrTooManyGuests = errors.New("calendar: more guests than the space allows")
	ErrModeMismatch  = errors.New("calendar: wrong quote type for this listing")
	ErrAdvanceMonths = errors.New("calendar: term is shorter than the required advance")
	ErrNotBookable   = errors.New("calendar: listing is not accepting bookings")
)

// Day strips a timestamp to a calendar date at UTC midnight. Stay dates are
// calendar facts, not instants: 12 June is 12 June regardless of where the
// server, the host or the guest happens to be.
func Day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Nights counts whole days in [checkIn, checkOut). Both arguments must already
// be Day-normalized, which keeps this exact across DST boundaries.
func Nights(checkIn, checkOut time.Time) int {
	return int(checkOut.Sub(checkIn) / (24 * time.Hour))
}

// RatePlan is a price window. The database guarantees no two plans for one
// space overlap, so at most one ever covers a given night.
type RatePlan struct {
	ID         uuid.UUID
	SpaceID    uuid.UUID
	Name       string
	StartsOn   time.Time
	EndsOn     time.Time // exclusive
	PriceMinor int64
	MinNights  *int
}

func (p RatePlan) Covers(day time.Time) bool {
	return !day.Before(p.StartsOn) && day.Before(p.EndsOn)
}

type Block struct {
	ID       uuid.UUID
	SpaceID  uuid.UUID
	StartsOn time.Time
	EndsOn   time.Time // exclusive
	Reason   string
}

func (b Block) Covers(day time.Time) bool {
	return !day.Before(b.StartsOn) && day.Before(b.EndsOn)
}

type StayRules struct {
	MinNights          int
	MaxNights          int // 0 means no ceiling
	WeeklyDiscountBps  int
	MonthlyDiscountBps int
	CleaningFeeMinor   int64
	CancellationPolicy string
}

func DefaultStayRules() StayRules {
	return StayRules{MinNights: 1, CancellationPolicy: "moderate"}
}

// SpacePricing is what the engine needs to know about a listing. Declared here
// so calendar never imports listing.
type SpacePricing struct {
	SpaceID        uuid.UUID
	Mode           string // "nightly" or "term"
	Status         string
	Currency       string
	BasePriceMinor int64
	PricePeriod    string
	DepositMinor   int64
	AdvanceMonths  int
	MaxOccupancy   int
}

// NightPrice is one date and what it costs. The client renders these straight
// into the calendar, so the engine returns the breakdown rather than a bare total.
type NightPrice struct {
	Date       time.Time `json:"date"`
	PriceMinor int64     `json:"price_minor"`
	Source     string    `json:"source"`
	Available  bool      `json:"available"`
}

type Line struct {
	Code        string `json:"code"`
	Label       string `json:"label"`
	AmountMinor int64  `json:"amount_minor"`
}

type Quote struct {
	Mode          string       `json:"mode"`
	Currency      string       `json:"currency"`
	Policy        string       `json:cancellation_policy`
	CheckIn       *time.Time   `json:"check_in,omitempty"`
	CheckOut      *time.Time   `json:"check_out,omitempty"`
	Nights        int          `json:"nights,omitempty"`
	Months        int          `json:"months,omitempty"`
	Lines         []Line       `json:"lines"`
	SubtotalMinor int64        `json:"subtotal_minor"`
	DiscountMinor int64        `json:"discount_minor"`
	FeesMinor     int64        `json:"fees_minor"`
	TotalMinor    int64        `json:"total_minor"`
	DueNowMinor   int64        `json:"due_now_minor"`
	Nightly       []NightPrice `json:"nightly,omitempty"`
}

// applyBps multiplies by basis points using integer arithmetic. Floats would
// make the same quote differ between the API, the web client and the ledger,
// and you discover that during a payout dispute.
func applyBps(amount int64, bps int) int64 {
	if bps <= 0 || amount <= 0 {
		return 0
	}
	return amount * int64(bps) / BasisPoints
}
