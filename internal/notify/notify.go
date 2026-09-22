package notify

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	EventBookingHeld      = "booking.held"
	EventBookingApproved  = "booking.approved"
	EventBookingConfirmed = "booking.confirmed"
	EventBookingCancelled = "booking.cancelled"
	EventMessageReceived  = "message.received"
	EventPayoutPaid       = "payout.paid"
	EventPayoutReversed   = "payout.reversed"
	EventMediaFailed      = "media.failed"
	EventReviewReceived   = "review.received"
)

// smsByDefault decides who gets interrupted. Each SMS costs money and a
// notification someone did not want teaches them to ignore the ones that matter.
var smsByDefault = map[string]bool{
	EventBookingHeld:      true,
	EventBookingApproved:  true,
	EventBookingConfirmed: true,
	EventBookingCancelled: true,
	EventPayoutPaid:       true,
	EventPayoutReversed:   true,
	EventMessageReceived:  false,
	EventMediaFailed:      false,
	EventReviewReceived:   false,
}

func SMSDefault(event string) bool { return smsByDefault[event] }

func KnownEvent(event string) bool {
	_, ok := smsByDefault[event]
	return ok
}

// Event is what a domain hands in. Key makes it idempotent: the same key for
// the same user produces one notification however often it is raised.
type Event struct {
	UserID uuid.UUID
	Kind   string
	Key    string
	Title  string
	Body   string
	SMS    string
	Data   map[string]any
}

type Notification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Event     string
	Title     string
	Body      string
	SMS       string
	Data      map[string]any
	ReadAt    *time.Time
	CreatedAt time.Time
}

var (
	ErrNotFound     = errors.New("notify: not found")
	ErrUnknownEvent = errors.New("notify: unknown event")
)

// FormatGHS renders minor units for humans. "GHS" rather than the cedi sign on
// purpose: the sign is outside GSM-7 and forces a whole SMS into UCS-2.
func FormatGHS(minor int64) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	whole := strconv.FormatInt(minor/100, 10)

	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return fmt.Sprintf("%sGHS %s.%02d", sign, b.String(), minor%100)
}

// The templates. Each returns the three renderings of one event: a short title,
// the fuller in-app body, and an SMS that must fit a single GSM-7 segment.

func BookingConfirmedGuest(ref string, startsOn time.Time, paidMinor int64) Event {
	date := startsOn.Format("2 Jan 2006")
	return Event{
		Kind:  EventBookingConfirmed,
		Title: "Booking confirmed",
		Body:  fmt.Sprintf("Your booking %s is confirmed, starting %s. You paid %s.", ref, date, FormatGHS(paidMinor)),
		SMS:   fmt.Sprintf("Space: booking %s confirmed for %s. Paid %s.", ref, date, FormatGHS(paidMinor)),
	}
}

func BookingConfirmedHost(ref string, startsOn time.Time) Event {
	date := startsOn.Format("2 Jan 2006")
	return Event{
		Kind:  EventBookingConfirmed,
		Title: "New confirmed booking",
		Body:  fmt.Sprintf("Booking %s is paid and confirmed, starting %s.", ref, date),
		SMS:   fmt.Sprintf("Space: new booking %s confirmed for %s.", ref, date),
	}
}

func PayoutPaid(amountMinor int64) Event {
	return Event{
		Kind:  EventPayoutPaid,
		Title: "Payout sent",
		Body:  fmt.Sprintf("%s has been sent to your mobile money account.", FormatGHS(amountMinor)),
		SMS:   fmt.Sprintf("Space: %s sent to your mobile money.", FormatGHS(amountMinor)),
	}
}

func PayoutReversed(amountMinor int64) Event {
	return Event{
		Kind:  EventPayoutReversed,
		Title: "Payout returned",
		Body: fmt.Sprintf("Your payout of %s could not be delivered and was returned to your balance. "+
			"Check your mobile money details and try again.", FormatGHS(amountMinor)),
		SMS: fmt.Sprintf("Space: payout of %s failed and was returned. Check your MoMo details.", FormatGHS(amountMinor)),
	}
}

func MessageReceived(preview string) Event {
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	return Event{
		Kind:  EventMessageReceived,
		Title: "New message",
		Body:  preview,
		SMS:   "Space: you have a new message.",
	}
}

func MediaFailed() Event {
	return Event{
		Kind:  EventMediaFailed,
		Title: "A photo could not be used",
		Body:  "One of your listing photos could not be read. Try uploading it again as a JPEG or PNG.",
		SMS:   "Space: a listing photo failed. Please upload it again.",
	}
}
