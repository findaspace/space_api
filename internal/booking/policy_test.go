package booking

import (
	"testing"
	"time"
)

var now = time.Date(2026, 6, 1, 14, 30, 0, 0, time.UTC)

func in(policy Policy, daysAhead int) RefundInput {
	return RefundInput{
		Policy: policy, Mode: ModeNightly, Status: StatusConfirmed, Actor: ActorGuest,
		DueNowMinor: 110000, FeesMinor: 10000,
		StartsOn: now.AddDate(0, 0, daysAhead), Now: now,
	}
}

func TestFlexible(t *testing.T) {
	if got := ComputeRefund(in(PolicyFlexible, 2)).RefundMinor; got != 100000 {
		t.Errorf("2 days out = %d, want 100000", got)
	}
	if got := ComputeRefund(in(PolicyFlexible, 0)).RefundMinor; got != 0 {
		t.Errorf("same day = %d, want 0", got)
	}
}

func TestModerate(t *testing.T) {
	cases := []struct {
		days int
		want int64
	}{
		{10, 100000},
		{5, 100000},
		{4, 50000},
		{1, 50000},
		{0, 0},
	}
	for _, tc := range cases {
		if got := ComputeRefund(in(PolicyModerate, tc.days)).RefundMinor; got != tc.want {
			t.Errorf("%d days out = %d, want %d", tc.days, got, tc.want)
		}
	}
}

func TestStrict(t *testing.T) {
	if got := ComputeRefund(in(PolicyStrict, 8)).RefundMinor; got != 50000 {
		t.Errorf("8 days out = %d, want 50000", got)
	}
	if got := ComputeRefund(in(PolicyStrict, 6)).RefundMinor; got != 0 {
		t.Errorf("6 days out = %d, want 0", got)
	}
}

// The service fee is excluded from every guest refund, on every policy.
func TestServiceFeeNeverRefundedToGuest(t *testing.T) {
	for _, p := range []Policy{PolicyFlexible, PolicyModerate, PolicyStrict} {
		r := ComputeRefund(in(p, 30))
		if r.RefundMinor > 100000 {
			t.Errorf("%s refunded the fee: %d", p, r.RefundMinor)
		}
	}
}

// A host cancelling makes the guest whole, fee included.
func TestHostCancellationRefundsEverything(t *testing.T) {
	i := in(PolicyStrict, 0)
	i.Actor = ActorHost

	if got := ComputeRefund(i).RefundMinor; got != 110000 {
		t.Errorf("host cancellation = %d, want 110000", got)
	}
}

func TestNothingCapturedYet(t *testing.T) {
	for _, s := range []Status{StatusHeld, StatusApproved} {
		i := in(PolicyFlexible, 30)
		i.Status = s
		if got := ComputeRefund(i).RefundMinor; got != 0 {
			t.Errorf("%s refunded %d, want 0", s, got)
		}
	}
}

func TestTermBeforeStartRefundsDepositAndAdvance(t *testing.T) {
	i := RefundInput{
		Policy: PolicyStrict, Mode: ModeTerm, Status: StatusConfirmed, Actor: ActorGuest,
		DueNowMinor: 6390000, FeesMinor: 540000,
		StartsOn: now.AddDate(0, 0, 2), Now: now,
	}
	if got := ComputeRefund(i).RefundMinor; got != 5850000 {
		t.Errorf("term refund = %d, want 5850000", got)
	}
}

func TestActiveLeaseIsNotAutoRefunded(t *testing.T) {
	i := RefundInput{
		Policy: PolicyFlexible, Mode: ModeTerm, Status: StatusActive, Actor: ActorGuest,
		DueNowMinor: 6390000, FeesMinor: 540000,
		StartsOn: now.AddDate(0, -2, 0), Now: now,
	}
	if got := ComputeRefund(i).RefundMinor; got != 0 {
		t.Errorf("active lease refunded %d, want 0", got)
	}
}

// 14:30 today to 00:00 tomorrow is one day, not zero.
func TestDaysUntilUsesCalendarDays(t *testing.T) {
	if got := daysUntil(now.AddDate(0, 0, 1), now); got != 1 {
		t.Errorf("daysUntil = %d, want 1", got)
	}
	if got := daysUntil(now, now); got != 0 {
		t.Errorf("same day = %d, want 0", got)
	}
}
