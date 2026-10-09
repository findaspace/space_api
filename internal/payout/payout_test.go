package payout

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGuessNetwork(t *testing.T) {
	cases := map[string]string{
		"+233244123456": NetworkMTN,
		"+233541234567": NetworkMTN,
		"+233201234567": NetworkTelecel,
		"+233501234567": NetworkTelecel,
		"+233271234567": NetworkAirtelTigo,
		"+233571234567": NetworkAirtelTigo,
	}
	for phone, want := range cases {
		got, ok := GuessNetwork(phone)
		if !ok || got != want {
			t.Errorf("GuessNetwork(%s) = %q, %v; want %q", phone, got, ok, want)
		}
	}

	for _, phone := range []string{"+2348012345678", "+233", "not a number", ""} {
		if _, ok := GuessNetwork(phone); ok {
			t.Errorf("GuessNetwork(%q) should not have matched", phone)
		}
	}
}

func TestValidNetwork(t *testing.T) {
	for _, code := range []string{NetworkMTN, NetworkTelecel, NetworkAirtelTigo} {
		if !ValidNetwork(code) {
			t.Errorf("%s should be valid", code)
		}
	}
	// Telecel rebranded from Vodafone; the Paystack code did not follow.
	if ValidNetwork("TELECEL") || ValidNetwork("mtn") || ValidNetwork("") {
		t.Error("accepted an invalid network code")
	}
}

func TestValidateAmount(t *testing.T) {
	cases := []struct {
		name              string
		amount, available int64
		want              error
	}{
		{"ok", 50000, 90000, nil},
		{"exactly the balance", 90000, 90000, nil},
		{"one pesewa over", 90001, 90000, ErrInsufficient},
		{"below minimum", 99, 90000, ErrBelowMinimum},
		{"at minimum", 100, 90000, nil},
		{"above maximum", MaxPayoutMinor + 1, MaxPayoutMinor * 2, ErrAboveMaximum},
		{"zero", 0, 90000, ErrBelowMinimum},
		{"negative", -1, 90000, ErrBelowMinimum},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateAmount(tc.amount, tc.available); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReleasable(t *testing.T) {
	checkIn := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		now  time.Time
		want bool
	}{
		{"day before check-in", time.Date(2026, 6, 9, 23, 0, 0, 0, time.UTC), false},
		{"check-in day", checkIn, false},
		{"one day after", time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC), false},
		{"exactly the hold", time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC), true},
		{"well after", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Releasable(checkIn, 2, tc.now); got != tc.want {
				t.Errorf("Releasable = %v, want %v", got, tc.want)
			}
		})
	}
}

// Time of day must not affect the decision; these are calendar days.
func TestReleasableIgnoresClockTime(t *testing.T) {
	checkIn := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	early := time.Date(2026, 6, 12, 0, 1, 0, 0, time.UTC)
	late := time.Date(2026, 6, 12, 23, 59, 0, 0, time.UTC)

	if !Releasable(checkIn, 2, early) || !Releasable(checkIn, 2, late) {
		t.Error("release should depend on the date, not the hour")
	}
}

func TestReferenceIsStable(t *testing.T) {
	id := mustUUID(t)
	first := NewReference(id)
	if first != NewReference(id) {
		t.Fatal("the same payout must always produce the same reference")
	}
	if len(NewReference(id)) != 36 { // "pot-" plus 32 hex characters
		t.Errorf("reference = %q", NewReference(id))
	}
}

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	return uuid.MustParse("01a0c0ee-8991-779c-a683-89807743044c")
}
