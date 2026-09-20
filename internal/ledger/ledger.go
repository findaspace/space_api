package ledger

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type Account string

const (
	// Assets: a debit increases them.
	AccountPSPClearing Account = "psp_clearing"
	AccountBank        Account = "bank"

	// Liabilities and income: a credit increases them.
	AccountHostPayable     Account = "host_payable"
	AccountPlatformRevenue Account = "platform_revenue"
	AccountRefundsPayable  Account = "refunds_payable"
)

type Direction string

const (
	Debit  Direction = "debit"
	Credit Direction = "credit"
)

type Kind string

const (
	KindCharge     Kind = "charge"
	KindRefund     Kind = "refund"
	KindPayout     Kind = "payout"
	KindSettlement Kind = "settlement"
	KindReversal   Kind = "reversal"
)

type Entry struct {
	Account   Account
	Direction Direction
	Amount    int64
	PartyID   *uuid.UUID
}

// Posting is one balanced set of entries. It is the only thing that reaches the
// database: entries are never written individually.
type Posting struct {
	Kind        Kind
	BookingID   *uuid.UUID
	Currency    string
	Memo        string
	ExternalRef string
	Entries     []Entry
}

var (
	ErrUnbalanced   = errors.New("ledger: posting does not balance")
	ErrEmptyPosting = errors.New("ledger: posting has no entries")
	ErrBadAmount    = errors.New("ledger: amounts must be positive")
	ErrDuplicate    = errors.New("ledger: posting already recorded")
)

// Validate is the same check the database trigger performs, run early so a bug
// surfaces in the service with a useful message rather than at COMMIT.
func (p Posting) Validate() error {
	if len(p.Entries) < 2 {
		return ErrEmptyPosting
	}

	var net int64
	for _, e := range p.Entries {
		if e.Amount <= 0 {
			return fmt.Errorf("%w: %s %s", ErrBadAmount, e.Account, e.Direction)
		}
		if e.Direction == Debit {
			net += e.Amount
		} else {
			net -= e.Amount
		}
	}
	if net != 0 {
		return fmt.Errorf("%w: off by %d", ErrUnbalanced, net)
	}
	return nil
}

func party(id uuid.UUID) *uuid.UUID { return &id }

// Charge records a guest payment. Money arrives at the PSP; the host is owed
// their share and the platform books its fee as revenue immediately.
//
//	DR psp_clearing      net + fee
//	CR host_payable      net
//	CR platform_revenue  fee
func Charge(bookingID *uuid.UUID, hostID uuid.UUID, currency, ref string, netMinor, feeMinor int64) Posting {
	entries := []Entry{
		{Account: AccountPSPClearing, Direction: Debit, Amount: netMinor + feeMinor},
		{Account: AccountHostPayable, Direction: Credit, Amount: netMinor, PartyID: party(hostID)},
	}
	if feeMinor > 0 {
		entries = append(entries,
			Entry{Account: AccountPlatformRevenue, Direction: Credit, Amount: feeMinor})
	}

	return Posting{
		Kind: KindCharge, BookingID: bookingID, Currency: currency,
		ExternalRef: ref, Memo: "guest payment",
		Entries: entries,
	}
}

// Refund reverses part of a charge. The host's share is clawed back, and the
// platform's fee is clawed back only when the platform is giving it up, which
// is the host-cancellation case.
//
//	DR host_payable      fromHost
//	DR platform_revenue  fromPlatform
//	CR refunds_payable   fromHost + fromPlatform
//
// The credit lands in refunds_payable rather than psp_clearing because
// approving a refund and actually sending the money are separate events.
func Refund(bookingID *uuid.UUID, hostID uuid.UUID, currency, ref string, fromHostMinor, fromPlatformMinor int64) Posting {
	entries := make([]Entry, 0, 3)
	if fromHostMinor > 0 {
		entries = append(entries,
			Entry{Account: AccountHostPayable, Direction: Debit, Amount: fromHostMinor, PartyID: party(hostID)})
	}
	if fromPlatformMinor > 0 {
		entries = append(entries,
			Entry{Account: AccountPlatformRevenue, Direction: Debit, Amount: fromPlatformMinor})
	}
	entries = append(entries, Entry{
		Account: AccountRefundsPayable, Direction: Credit,
		Amount: fromHostMinor + fromPlatformMinor,
	})

	return Posting{
		Kind: KindRefund, BookingID: bookingID, Currency: currency,
		ExternalRef: ref, Memo: "guest refund",
		Entries: entries,
	}
}

// RefundSettled records the refund actually leaving the PSP balance.
//
//	DR refunds_payable  amount
//	CR psp_clearing     amount
func RefundSettled(bookingID *uuid.UUID, currency, ref string, amountMinor int64) Posting {
	return Posting{
		Kind: KindReversal, BookingID: bookingID, Currency: currency,
		ExternalRef: ref, Memo: "refund sent",
		Entries: []Entry{
			{Account: AccountRefundsPayable, Direction: Debit, Amount: amountMinor},
			{Account: AccountPSPClearing, Direction: Credit, Amount: amountMinor},
		},
	}
}

// Payout sends a host what they are owed.
//
//	DR host_payable   amount
//	CR psp_clearing   amount
func Payout(hostID uuid.UUID, currency, ref string, amountMinor int64) Posting {
	return Posting{
		Kind: KindPayout, Currency: currency, ExternalRef: ref,
		Memo: "host payout",
		Entries: []Entry{
			{Account: AccountHostPayable, Direction: Debit, Amount: amountMinor, PartyID: party(hostID)},
			{Account: AccountPSPClearing, Direction: Credit, Amount: amountMinor},
		},
	}
}

// Settlement moves money from the PSP's hands into our bank account. Without
// this, psp_clearing grows forever and your cash position is fiction.
//
//	DR bank           amount
//	CR psp_clearing   amount
func Settlement(currency, ref string, amountMinor int64) Posting {
	return Posting{
		Kind: KindSettlement, Currency: currency, ExternalRef: ref,
		Memo: "psp settlement",
		Entries: []Entry{
			{Account: AccountBank, Direction: Debit, Amount: amountMinor},
			{Account: AccountPSPClearing, Direction: Credit, Amount: amountMinor},
		},
	}
}

// SplitRefund decides how a refund is funded. A guest cancellation never costs
// the platform its fee; a host cancellation always does. Putting this next to
// the postings keeps the money rule and the bookkeeping in one file.
func SplitRefund(refundMinor, feeMinor int64, hostAtFault bool) (fromHost, fromPlatform int64) {
	if !hostAtFault {
		return refundMinor, 0
	}
	if refundMinor <= feeMinor {
		return 0, refundMinor
	}
	return refundMinor - feeMinor, feeMinor
}
