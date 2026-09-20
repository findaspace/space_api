package calendar

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

var today = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func d(day int) time.Time { return time.Date(2026, 6, day, 0, 0, 0, 0, time.UTC) }

func nightlySpace() SpacePricing {
	return SpacePricing{
		SpaceID: uuid.New(), Mode: "nightly", Status: "published",
		Currency: "GHS", BasePriceMinor: 20000, PricePeriod: "night",
		MaxOccupancy: 4,
	}
}

func baseInput() NightlyInput {
	return NightlyInput{
		Space:    nightlySpace(),
		CheckIn:  d(10),
		CheckOut: d(13),
		Guests:   2,
		Rules:    DefaultStayRules(),
		Today:    today,
	}
}

func TestNights(t *testing.T) {
	if got := Nights(d(10), d(13)); got != 3 {
		t.Errorf("Nights = %d, want 3", got)
	}
}

func TestQuoteNightlyBasic(t *testing.T) {
	q, err := QuoteNightly(baseInput())
	if err != nil {
		t.Fatal(err)
	}
	if q.Nights != 3 {
		t.Errorf("nights = %d, want 3", q.Nights)
	}
	if q.SubtotalMinor != 60000 {
		t.Errorf("subtotal = %d, want 60000", q.SubtotalMinor)
	}
	if q.TotalMinor != 60000 {
		t.Errorf("total = %d, want 60000", q.TotalMinor)
	}
	if len(q.Nightly) != 3 {
		t.Fatalf("nightly breakdown has %d entries", len(q.Nightly))
	}
	// Exclusive end: the 13th is the next guest's check-in, not this guest's night.
	if !q.Nightly[2].Date.Equal(d(12)) {
		t.Errorf("last night = %v, want %v", q.Nightly[2].Date, d(12))
	}
}

func TestRatePlanOverridesBasePrice(t *testing.T) {
	in := baseInput()
	in.Plans = []RatePlan{{
		Name: "Christmas", StartsOn: d(11), EndsOn: d(13), PriceMinor: 50000,
	}}

	q, err := QuoteNightly(in)
	if err != nil {
		t.Fatal(err)
	}
	// 20000 + 50000 + 50000
	if q.SubtotalMinor != 120000 {
		t.Errorf("subtotal = %d, want 120000", q.SubtotalMinor)
	}
	if q.Nightly[0].Source != "base" || q.Nightly[1].Source != "Christmas" {
		t.Errorf("sources = %q, %q", q.Nightly[0].Source, q.Nightly[1].Source)
	}
}

func TestRatePlanMinNightsWins(t *testing.T) {
	three := 3
	in := baseInput()
	in.CheckOut = d(12) // two nights
	in.Plans = []RatePlan{{Name: "Peak", StartsOn: d(10), EndsOn: d(20), PriceMinor: 30000, MinNights: &three}}

	if _, err := QuoteNightly(in); !errors.Is(err, ErrTooShort) {
		t.Fatalf("err = %v, want ErrTooShort", err)
	}
}

// A plan's minimum must not apply to a stay that never touches it.
func TestRatePlanMinNightsIgnoredOutsideWindow(t *testing.T) {
	ten := 10
	in := baseInput()
	in.Plans = []RatePlan{{Name: "Festival", StartsOn: d(20), EndsOn: d(25), PriceMinor: 90000, MinNights: &ten}}

	if _, err := QuoteNightly(in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlockedNightRejected(t *testing.T) {
	in := baseInput()
	in.Blocks = []Block{{StartsOn: d(11), EndsOn: d(12)}}

	if _, err := QuoteNightly(in); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// A block ending on the check-in date does not touch the stay, because both
// ends are exclusive. This is the off-by-one that bites every booking system.
func TestBlockEndingOnCheckInIsFine(t *testing.T) {
	in := baseInput()
	in.Blocks = []Block{{StartsOn: d(5), EndsOn: d(10)}}

	if _, err := QuoteNightly(in); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWeeklyAndMonthlyDiscounts(t *testing.T) {
	in := baseInput()
	in.Rules.WeeklyDiscountBps = 1000  // 10%
	in.Rules.MonthlyDiscountBps = 2000 // 20%

	in.CheckOut = d(18) // 8 nights, 160000
	q, err := QuoteNightly(in)
	if err != nil {
		t.Fatal(err)
	}
	if q.DiscountMinor != 16000 {
		t.Errorf("weekly discount = %d, want 16000", q.DiscountMinor)
	}

	in.CheckOut = time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC) // 30 nights, 600000
	q, err = QuoteNightly(in)
	if err != nil {
		t.Fatal(err)
	}
	if q.DiscountMinor != 120000 {
		t.Errorf("monthly discount = %d, want 120000", q.DiscountMinor)
	}
}

// The cleaning fee is the same work regardless of stay length, so the
// length-of-stay discount must not touch it.
func TestDiscountDoesNotApplyToCleaningFee(t *testing.T) {
	in := baseInput()
	in.CheckOut = d(18) // 8 nights, 160000
	in.Rules.WeeklyDiscountBps = 1000
	in.Rules.CleaningFeeMinor = 5000

	q, err := QuoteNightly(in)
	if err != nil {
		t.Fatal(err)
	}
	// 160000 - 16000 + 5000
	if q.TotalMinor != 149000 {
		t.Errorf("total = %d, want 149000", q.TotalMinor)
	}
}

func TestPlatformFeeIsIntegerMath(t *testing.T) {
	in := baseInput()
	in.Space.BasePriceMinor = 3333
	in.PlatformFeeBps = 333 // 3.33%

	q, err := QuoteNightly(in)
	if err != nil {
		t.Fatal(err)
	}
	// 9999 * 333 / 10000 = 332 (floored), never 332.9667
	if q.FeesMinor != 332 {
		t.Errorf("fee = %d, want 332", q.FeesMinor)
	}
	if q.TotalMinor != 10331 {
		t.Errorf("total = %d, want 10331", q.TotalMinor)
	}
}

func TestNightlyRejections(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*NightlyInput)
		want error
	}{
		{"same day", func(i *NightlyInput) { i.CheckOut = i.CheckIn }, ErrInvalidRange},
		{"reversed", func(i *NightlyInput) { i.CheckIn, i.CheckOut = i.CheckOut, i.CheckIn }, ErrInvalidRange},
		{"past", func(i *NightlyInput) { i.CheckIn = d(1).AddDate(0, 0, -5); i.CheckOut = d(1) }, ErrPastDate},
		{"horizon", func(i *NightlyInput) {
			i.CheckIn = today.AddDate(0, 0, MaxHorizonDays+10)
			i.CheckOut = i.CheckIn.AddDate(0, 0, 2)
		}, ErrHorizon},
		{"too many guests", func(i *NightlyInput) { i.Guests = 9 }, ErrTooManyGuests},
		{"below min nights", func(i *NightlyInput) { i.CheckOut = d(11); i.Rules.MinNights = 2 }, ErrTooShort},
		{"above max nights", func(i *NightlyInput) { i.Rules.MaxNights = 2 }, ErrTooLong},
		{"term listing", func(i *NightlyInput) { i.Space.Mode = "term" }, ErrModeMismatch},
		{"draft listing", func(i *NightlyInput) { i.Space.Status = "draft" }, ErrNotBookable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput()
			tc.mut(&in)
			if _, err := QuoteNightly(in); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func termSpace() SpacePricing {
	return SpacePricing{
		SpaceID: uuid.New(), Mode: "term", Status: "published",
		Currency: "GHS", BasePriceMinor: 450000, PricePeriod: "month",
		DepositMinor: 450000, AdvanceMonths: 12, MaxOccupancy: 4,
	}
}

func TestQuoteTermUpfrontCost(t *testing.T) {
	q, err := QuoteTerm(TermInput{
		Space: termSpace(), StartsOn: d(10), Months: 12, Today: today,
	})
	if err != nil {
		t.Fatal(err)
	}
	if q.TotalMinor != 5400000 {
		t.Errorf("total = %d, want 5400000", q.TotalMinor)
	}
	// deposit 450000 + twelve months 5400000
	if q.DueNowMinor != 5850000 {
		t.Errorf("due now = %d, want 5850000", q.DueNowMinor)
	}
}

func TestQuoteTermRejectsShortTerm(t *testing.T) {
	_, err := QuoteTerm(TermInput{
		Space: termSpace(), StartsOn: d(10), Months: 6, Today: today,
	})
	if !errors.Is(err, ErrAdvanceMonths) {
		t.Fatalf("err = %v, want ErrAdvanceMonths", err)
	}
}

func TestQuoteTermYearlyPriceIsMonthly(t *testing.T) {
	sp := termSpace()
	sp.PricePeriod = "year"
	sp.BasePriceMinor = 6000000
	sp.AdvanceMonths = 1
	sp.DepositMinor = 0

	q, err := QuoteTerm(TermInput{Space: sp, StartsOn: d(10), Months: 12, Today: today})
	if err != nil {
		t.Fatal(err)
	}
	if q.Lines[0].AmountMinor != 500000 {
		t.Errorf("monthly rent = %d, want 500000", q.Lines[0].AmountMinor)
	}
}
