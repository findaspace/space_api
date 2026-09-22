package notify

import (
	"testing"
	"time"
)

func TestFormatGHS(t *testing.T) {
	cases := map[int64]string{
		0:         "GHS 0.00",
		5:         "GHS 0.05",
		100:       "GHS 1.00",
		450000:    "GHS 4,500.00",
		5850000:   "GHS 58,500.00",
		123456789: "GHS 1,234,567.89",
		-100000:   "-GHS 1,000.00",
	}
	for in, want := range cases {
		if got := FormatGHS(in); got != want {
			t.Errorf("FormatGHS(%d) = %q, want %q", in, got, want)
		}
	}
}

// One non-GSM character pushes a whole message into UCS-2: 70 characters per
// segment instead of 160, at roughly triple the cost. This keeps every template
// inside a single cheap segment.
func TestSMSTemplatesFitOneGSM7Segment(t *testing.T) {
	start := time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)
	templates := map[string]Event{
		"confirmed guest": BookingConfirmedGuest("SPC-S53KAWW3", start, 999999999),
		"confirmed host":  BookingConfirmedHost("SPC-S53KAWW3", start),
		"payout paid":     PayoutPaid(999999999),
		"payout reversed": PayoutReversed(999999999),
		"message":         MessageReceived("hello"),
		"media failed":    MediaFailed(),
	}

	for name, e := range templates {
		for i, r := range e.SMS {
			if r > 127 {
				t.Errorf("%s: non-ASCII %q at %d forces UCS-2 encoding", name, r, i)
			}
		}
		if len(e.SMS) > 160 {
			t.Errorf("%s: %d characters, over one segment: %q", name, len(e.SMS), e.SMS)
		}
	}
}

func TestEverySMSDefaultIsKnown(t *testing.T) {
	for _, e := range []string{
		EventBookingConfirmed, EventPayoutPaid, EventMessageReceived, EventMediaFailed,
	} {
		if !KnownEvent(e) {
			t.Errorf("%s has no default", e)
		}
	}
	if SMSDefault(EventMessageReceived) {
		t.Error("chat messages must not text by default")
	}
	if !SMSDefault(EventBookingConfirmed) {
		t.Error("booking confirmations must text by default")
	}
}
