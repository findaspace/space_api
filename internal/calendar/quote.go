package calendar

import "time"

// ResolveNights prices each night in [checkIn, checkOut). At most one rate plan
// can cover a night, so resolution is a lookup rather than a precedence fight.
func ResolveNights(checkIn, checkOut time.Time, basePriceMinor int64, plans []RatePlan, blocks []Block) []NightPrice {
	out := make([]NightPrice, 0, Nights(checkIn, checkOut))

	for day := checkIn; day.Before(checkOut); day = day.AddDate(0, 0, 1) {
		night := NightPrice{
			Date:       day,
			PriceMinor: basePriceMinor,
			Source:     "base",
			Available:  true,
		}

		for _, p := range plans {
			if p.Covers(day) {
				night.PriceMinor = p.PriceMinor
				night.Source = p.Name
				if night.Source == "" {
					night.Source = "rate_plan"
				}
				break
			}
		}

		for _, b := range blocks {
			if b.Covers(day) {
				night.Available = false
				break
			}
		}

		out = append(out, night)
	}
	return out
}

// effectiveMinNights takes the strictest minimum that applies. A peak-season
// plan demanding three nights must not be undercut by the space default of one.
func effectiveMinNights(rules StayRules, checkIn, checkOut time.Time, plans []RatePlan) int {
	minNights := rules.MinNights
	if minNights < 1 {
		minNights = 1
	}

	for _, p := range plans {
		if p.MinNights == nil || *p.MinNights <= minNights {
			continue
		}
		// Only plans that actually cover a night of this stay apply.
		if p.StartsOn.Before(checkOut) && checkIn.Before(p.EndsOn) {
			minNights = *p.MinNights
		}
	}
	return minNights
}

type NightlyInput struct {
	Space          SpacePricing
	CheckIn        time.Time
	CheckOut       time.Time
	Guests         int
	Rules          StayRules
	Plans          []RatePlan
	Blocks         []Block
	PlatformFeeBps int
	Today          time.Time
}

func QuoteNightly(in NightlyInput) (Quote, error) {
	if in.Space.Mode != "nightly" {
		return Quote{}, ErrModeMismatch
	}
	if in.Space.Status != "published" {
		return Quote{}, ErrNotBookable
	}

	checkIn, checkOut, today := Day(in.CheckIn), Day(in.CheckOut), Day(in.Today)

	if !checkOut.After(checkIn) {
		return Quote{}, ErrInvalidRange
	}
	if checkIn.Before(today) {
		return Quote{}, ErrPastDate
	}
	if checkIn.After(today.AddDate(0, 0, MaxHorizonDays)) {
		return Quote{}, ErrHorizon
	}

	nights := Nights(checkIn, checkOut)
	if nights > MaxStayNights {
		return Quote{}, ErrTooLong
	}
	if in.Guests > in.Space.MaxOccupancy {
		return Quote{}, ErrTooManyGuests
	}

	minNights := effectiveMinNights(in.Rules, checkIn, checkOut, in.Plans)
	if nights < minNights {
		return Quote{}, ErrTooShort
	}
	if in.Rules.MaxNights > 0 && nights > in.Rules.MaxNights {
		return Quote{}, ErrTooLong
	}

	nightly := ResolveNights(checkIn, checkOut, in.Space.BasePriceMinor, in.Plans, in.Blocks)

	var subtotal int64
	for _, n := range nightly {
		if !n.Available {
			return Quote{}, ErrUnavailable
		}
		subtotal += n.PriceMinor
	}

	// Length-of-stay discount applies to the nights only, never to the cleaning
	// fee: the host does the same amount of cleaning for a long stay as a short one.
	discountBps := 0
	discountLabel := ""
	switch {
	case nights >= MonthlyThreshold && in.Rules.MonthlyDiscountBps > 0:
		discountBps, discountLabel = in.Rules.MonthlyDiscountBps, "Monthly discount"
	case nights >= WeeklyThreshold && in.Rules.WeeklyDiscountBps > 0:
		discountBps, discountLabel = in.Rules.WeeklyDiscountBps, "Weekly discount"
	}
	discount := applyBps(subtotal, discountBps)

	afterDiscount := subtotal - discount
	platformFee := applyBps(afterDiscount+in.Rules.CleaningFeeMinor, in.PlatformFeeBps)

	lines := []Line{{
		Code:        "nights",
		Label:       "Nightly rate",
		AmountMinor: subtotal,
	}}
	if discount > 0 {
		lines = append(lines, Line{Code: "discount", Label: discountLabel, AmountMinor: -discount})
	}
	if in.Rules.CleaningFeeMinor > 0 {
		lines = append(lines, Line{Code: "cleaning_fee", Label: "Cleaning fee", AmountMinor: in.Rules.CleaningFeeMinor})
	}
	if platformFee > 0 {
		lines = append(lines, Line{Code: "service_fee", Label: "Service fee", AmountMinor: platformFee})
	}

	total := afterDiscount + in.Rules.CleaningFeeMinor + platformFee

	return Quote{
		Mode:          "nightly",
		Currency:      in.Space.Currency,
		Policy:        in.Rules.CancellationPolicy,
		CheckIn:       &checkIn,
		CheckOut:      &checkOut,
		Nights:        nights,
		Lines:         lines,
		SubtotalMinor: subtotal,
		DiscountMinor: discount,
		FeesMinor:     in.Rules.CleaningFeeMinor + platformFee,
		TotalMinor:    total,
		DueNowMinor:   total,
		Nightly:       nightly,
	}, nil
}

type TermInput struct {
	Space          SpacePricing
	StartsOn       time.Time
	Months         int
	Blocks         []Block
	PlatformFeeBps int
	Today          time.Time
	Policy         string
}

// QuoteTerm prices a lease. The interesting number is not the total, it is
// DueNow: Ghanaian landlords routinely require a year or two of rent up front,
// and a tenant who only learns that at signing has wasted everyone's time.
func QuoteTerm(in TermInput) (Quote, error) {
	if in.Space.Mode != "term" {
		return Quote{}, ErrModeMismatch
	}
	if in.Space.Status != "published" {
		return Quote{}, ErrNotBookable
	}

	start, today := Day(in.StartsOn), Day(in.Today)

	if in.Months < 1 {
		return Quote{}, ErrInvalidRange
	}
	if start.Before(today) {
		return Quote{}, ErrPastDate
	}
	if start.After(today.AddDate(0, 0, MaxHorizonDays)) {
		return Quote{}, ErrHorizon
	}

	advance := in.Space.AdvanceMonths
	if advance < 1 {
		advance = 1
	}
	if in.Months < advance {
		return Quote{}, ErrAdvanceMonths
	}

	end := start.AddDate(0, in.Months, 0)
	for _, b := range in.Blocks {
		if b.StartsOn.Before(end) && start.Before(b.EndsOn) {
			return Quote{}, ErrUnavailable
		}
	}

	rent := in.Space.BasePriceMinor
	if in.Space.PricePeriod == "year" {
		rent = rent / 12
	}

	total := rent * int64(in.Months)
	upfront := rent * int64(advance)
	platformFee := applyBps(upfront, in.PlatformFeeBps)

	lines := []Line{
		{Code: "rent", Label: "Monthly rent", AmountMinor: rent},
		{Code: "advance", Label: "Rent in advance", AmountMinor: upfront},
	}
	if in.Space.DepositMinor > 0 {
		lines = append(lines, Line{Code: "deposit", Label: "Security deposit", AmountMinor: in.Space.DepositMinor})
	}
	if platformFee > 0 {
		lines = append(lines, Line{Code: "service_fee", Label: "Service fee", AmountMinor: platformFee})
	}

	return Quote{
		Mode:          "term",
		Currency:      in.Space.Currency,
		Policy:        in.Policy,
		CheckIn:       &start,
		CheckOut:      &end,
		Months:        in.Months,
		Lines:         lines,
		SubtotalMinor: total,
		FeesMinor:     platformFee,
		TotalMinor:    total,
		DueNowMinor:   in.Space.DepositMinor + upfront + platformFee,
	}, nil
}
