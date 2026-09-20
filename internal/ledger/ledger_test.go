package ledger

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func sum(p Posting, account Account, dir Direction) int64 {
	var total int64
	for _, e := range p.Entries {
		if e.Account == account && e.Direction == dir {
			total += e.Amount
		}
	}
	return total
}

func TestEveryPostingBalances(t *testing.T) {
	booking, host := uuid.New(), uuid.New()

	postings := map[string]Posting{
		"charge":         Charge(&booking, host, "GHS", "ref-1", 90000, 10000),
		"charge no fee":  Charge(&booking, host, "GHS", "ref-2", 90000, 0),
		"refund guest":   Refund(&booking, host, "GHS", "ref-3", 50000, 0),
		"refund host":    Refund(&booking, host, "GHS", "ref-4", 90000, 10000),
		"refund settled": RefundSettled(&booking, "GHS", "ref-5", 50000),
		"payout":         Payout(host, "GHS", "ref-6", 90000),
		"settlement":     Settlement("GHS", "ref-7", 100000),
	}

	for name, p := range postings {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestChargeSplitsBetweenHostAndPlatform(t *testing.T) {
	booking, host := uuid.New(), uuid.New()
	p := Charge(&booking, host, "GHS", "ref", 90000, 10000)

	if got := sum(p, AccountPSPClearing, Debit); got != 100000 {
		t.Errorf("psp_clearing debit = %d, want 100000", got)
	}
	if got := sum(p, AccountHostPayable, Credit); got != 90000 {
		t.Errorf("host_payable credit = %d, want 90000", got)
	}
	if got := sum(p, AccountPlatformRevenue, Credit); got != 10000 {
		t.Errorf("platform_revenue credit = %d, want 10000", got)
	}
}

// The host's payable entry must be tagged, or per-host balances are impossible.
func TestHostEntriesCarryPartyID(t *testing.T) {
	host := uuid.New()
	p := Charge(nil, host, "GHS", "ref", 90000, 10000)

	for _, e := range p.Entries {
		if e.Account != AccountHostPayable {
			continue
		}
		if e.PartyID == nil || *e.PartyID != host {
			t.Fatalf("host_payable entry party = %v, want %s", e.PartyID, host)
		}
		return
	}
	t.Fatal("no host_payable entry")
}

func TestPlatformEntriesHaveNoParty(t *testing.T) {
	p := Charge(nil, uuid.New(), "GHS", "ref", 90000, 10000)

	for _, e := range p.Entries {
		if e.Account == AccountPlatformRevenue && e.PartyID != nil {
			t.Error("platform revenue must not be attributed to a user")
		}
	}
}

func TestSplitRefund(t *testing.T) {
	cases := []struct {
		name         string
		refund, fee  int64
		hostAtFault  bool
		wantHost     int64
		wantPlatform int64
	}{
		{"guest cancels, platform keeps fee", 90000, 10000, false, 90000, 0},
		{"host cancels, platform gives up fee", 100000, 10000, true, 90000, 10000},
		{"host cancels, refund smaller than fee", 5000, 10000, true, 0, 5000},
		{"nothing refunded", 0, 10000, false, 0, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, platform := SplitRefund(tc.refund, tc.fee, tc.hostAtFault)
			if host != tc.wantHost || platform != tc.wantPlatform {
				t.Errorf("= (%d, %d), want (%d, %d)", host, platform, tc.wantHost, tc.wantPlatform)
			}
			if host+platform != tc.refund {
				t.Errorf("split lost money: %d + %d != %d", host, platform, tc.refund)
			}
		})
	}
}

// A full charge then a full host-cancellation refund must leave both the host
// and the platform back at zero.
func TestChargeThenFullRefundNetsToZero(t *testing.T) {
	booking, host := uuid.New(), uuid.New()

	charge := Charge(&booking, host, "GHS", "c", 90000, 10000)
	fromHost, fromPlatform := SplitRefund(100000, 10000, true)
	refund := Refund(&booking, host, "GHS", "r", fromHost, fromPlatform)

	hostNet := sum(charge, AccountHostPayable, Credit) - sum(refund, AccountHostPayable, Debit)
	if hostNet != 0 {
		t.Errorf("host_payable net = %d, want 0", hostNet)
	}

	platformNet := sum(charge, AccountPlatformRevenue, Credit) - sum(refund, AccountPlatformRevenue, Debit)
	if platformNet != 0 {
		t.Errorf("platform_revenue net = %d, want 0", platformNet)
	}
}

func TestValidateRejectsUnbalanced(t *testing.T) {
	p := Posting{
		Kind: KindCharge, Currency: "GHS",
		Entries: []Entry{
			{Account: AccountPSPClearing, Direction: Debit, Amount: 100000},
			{Account: AccountHostPayable, Direction: Credit, Amount: 90000},
		},
	}
	if err := p.Validate(); !errors.Is(err, ErrUnbalanced) {
		t.Fatalf("err = %v, want ErrUnbalanced", err)
	}
}

func TestValidateRejectsNonPositiveAmounts(t *testing.T) {
	p := Posting{
		Kind: KindCharge, Currency: "GHS",
		Entries: []Entry{
			{Account: AccountPSPClearing, Direction: Debit, Amount: 0},
			{Account: AccountHostPayable, Direction: Credit, Amount: 0},
		},
	}
	if err := p.Validate(); !errors.Is(err, ErrBadAmount) {
		t.Fatalf("err = %v, want ErrBadAmount", err)
	}
}

func TestValidateRejectsSingleEntry(t *testing.T) {
	p := Posting{Kind: KindCharge, Currency: "GHS",
		Entries: []Entry{{Account: AccountBank, Direction: Debit, Amount: 100}}}

	if err := p.Validate(); !errors.Is(err, ErrEmptyPosting) {
		t.Fatalf("err = %v, want ErrEmptyPosting", err)
	}
}
