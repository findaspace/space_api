package booking

import "time"

type Policy string

const (
	PolicyFlexible Policy = "flexible"
	PolicyModerate Policy = "moderate"
	PolicyStrict   Policy = "strict"
)

func (p Policy) Valid() bool {
	switch p {
	case PolicyFlexible, PolicyModerate, PolicyStrict:
		return true
	}
	return false
}

type RefundInput struct {
	Policy      Policy
	Mode        Mode
	Status      Status
	Actor       Actor
	DueNowMinor int64
	FeesMinor   int64
	StartsOn    time.Time
	Now         time.Time
}

type RefundOutcome struct {
	RefundMinor int64  `json:"refund_minor"`
	Reason      string `json:"reason"`
}

// ComputeRefund is the only place money-back rules live. Pure, so the same
// numbers come out in the API, in a support tool, and in the ledger.
func ComputeRefund(in RefundInput) RefundOutcome {
	// Nothing has been captured before confirmation, so there is nothing to
	// return. The dates are released either way.
	if in.Status == StatusHeld || in.Status == StatusApproved {
		return RefundOutcome{0, "no payment had been taken"}
	}

	// A host who pulls out makes the guest whole, service fee included. The
	// platform eats its own fee rather than profiting from the host's failure.
	if in.Actor == ActorHost || in.Actor == ActorAdmin {
		return RefundOutcome{in.DueNowMinor, "cancelled by the host"}
	}

	// An in-progress lease is a termination, not a cancellation: notice periods
	// and dilapidations are a human decision, so this refunds nothing
	// automatically and the case goes to support.
	if in.Status == StatusActive {
		return RefundOutcome{0, "lease already started; handled as a termination"}
	}

	refundable := in.DueNowMinor - in.FeesMinor
	if refundable < 0 {
		refundable = 0
	}

	// A term booking cancelled before the lease begins returns the deposit and
	// the rent advance in full. Withholding a year's rent because a tenant
	// pulled out two months early is the practice this product exists to fix.
	if in.Mode == ModeTerm {
		return RefundOutcome{refundable, "cancelled before the lease started"}
	}

	days := daysUntil(in.StartsOn, in.Now)

	switch in.Policy {
	case PolicyFlexible:
		if days >= 1 {
			return RefundOutcome{refundable, "full refund, more than 24 hours before check-in"}
		}
		return RefundOutcome{0, "no refund within 24 hours of check-in"}

	case PolicyStrict:
		if days >= 7 {
			return RefundOutcome{refundable / 2, "half refund, more than 7 days before check-in"}
		}
		return RefundOutcome{0, "no refund within 7 days of check-in"}

	default: // moderate
		if days >= 5 {
			return RefundOutcome{refundable, "full refund, more than 5 days before check-in"}
		}
		if days >= 1 {
			return RefundOutcome{refundable / 2, "half refund, more than 24 hours before check-in"}
		}
		return RefundOutcome{0, "no refund within 24 hours of check-in"}
	}
}

// daysUntil counts whole calendar days from now to the start date. Both are
// normalized to UTC midnight first, so a stay starting tomorrow is one day away
// whether it is 00:05 or 23:55 right now.
func daysUntil(startsOn, now time.Time) int {
	day := func(t time.Time) time.Time {
		y, m, d := t.UTC().Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return int(day(startsOn).Sub(day(now)) / (24 * time.Hour))
}
