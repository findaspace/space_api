package payout

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/samnart1/space_api/internal/ledger"
	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/platform/paystack"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

type HostSource interface {
	DisplayNameFor(ctx context.Context, userID uuid.UUID) (name, phone string, err error)
}

type ServiceConfig struct {
	HoldDays int
	Currency string
}

type Service struct {
	store    *Store
	client   *paystack.Client
	hosts    HostSource
	log      *slog.Logger
	cfg      ServiceConfig
	notifier Notifier
}

type Notifier interface {
	NotifyTx(ctx context.Context, tx pgx.Tx, e notify.Event) error
}

func (s *Service) SetNotifier(n Notifier) { s.notifier = n }

func NewService(store *Store, client *paystack.Client, hosts HostSource,
	log *slog.Logger, cfg ServiceConfig) *Service {
	return &Service{store: store, client: client, hosts: hosts, log: log, cfg: cfg}
}

type AccountInput struct {
	Channel       Channel
	BankCode      string
	AccountNumber string
	AccountName   string
}

// SaveAccount stores where a host wants to be paid and registers it with
// Paystack. Registration is what actually validates that the number belongs to
// the claimed network; our prefix guess never does.
func (s *Service) SaveAccount(ctx context.Context, hostID uuid.UUID, in AccountInput) (Account, error) {
	if in.Channel == ChannelMobileMoney && !ValidNetwork(in.BankCode) {
		return Account{}, ErrUnknownNetwork
	}

	name := strings.TrimSpace(in.AccountName)
	if name == "" {
		display, _, err := s.hosts.DisplayNameFor(ctx, hostID)
		if err != nil {
			return Account{}, err
		}
		name = display
	}

	account, err := s.store.SaveAccount(ctx, Account{
		UserID:        hostID,
		Channel:       in.Channel,
		BankCode:      in.BankCode,
		AccountNumber: strings.TrimSpace(in.AccountNumber),
		AccountName:   name,
		Currency:      s.cfg.Currency,
	})
	if err != nil {
		return Account{}, err
	}

	if account.RecipientCode == "" {
		recipient, err := s.client.CreateRecipient(ctx, paystack.RecipientRequest{
			Type:          string(account.Channel),
			Name:          account.AccountName,
			AccountNumber: account.AccountNumber,
			BankCode:      account.BankCode,
			Currency:      account.Currency,
		})
		if err != nil {
			return Account{}, err
		}
		if err := s.store.SetRecipientCode(ctx, account.ID, recipient.RecipientCode); err != nil {
			return Account{}, err
		}
		account.RecipientCode = recipient.RecipientCode
	}
	return account, nil
}

func (s *Service) Account(ctx context.Context, hostID uuid.UUID) (Account, error) {
	return s.store.DefaultAccount(ctx, hostID)
}

func (s *Service) Balance(ctx context.Context, hostID uuid.UUID) (Balance, error) {
	return s.store.Balance(ctx, hostID, s.cfg.HoldDays)
}

func (s *Service) List(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Payout, error) {
	return s.store.ListForHost(ctx, hostID, limit, offset)
}

// Request withdraws to the host's default account.
func (s *Service) Request(ctx context.Context, hostID uuid.UUID, amountMinor int64) (Payout, error) {
	account, err := s.store.DefaultAccount(ctx, hostID)
	if err != nil {
		return Payout{}, err
	}
	if account.RecipientCode == "" {
		return Payout{}, ErrNoAccount
	}

	balance, err := s.store.Balance(ctx, hostID, s.cfg.HoldDays)
	if err != nil {
		return Payout{}, err
	}
	if err := ValidateAmount(amountMinor, balance.WithdrawableMinor); err != nil {
		return Payout{}, err
	}

	// The row exists before the transfer does, so a crash between the two
	// leaves a pending payout that reconciliation can resolve, rather than
	// money in flight with nothing recording it.
	p, err := s.store.CreateRequest(ctx, hostID, account.ID, s.cfg.Currency, amountMinor)
	if err != nil {
		return Payout{}, err
	}

	transfer, err := s.client.InitiateTransfer(ctx, paystack.TransferRequest{
		AmountMinor: amountMinor,
		Recipient:   account.RecipientCode,
		Reason:      "Space host payout",
		Reference:   p.Reference,
		Currency:    s.cfg.Currency,
	})
	if err != nil {
		// Marked failed rather than deleted: the reference must never be
		// reused for a different amount, and the attempt is worth keeping.
		if markErr := s.store.MarkFailed(ctx, p.ID, err.Error()); markErr != nil {
			s.log.ErrorContext(ctx, "could not mark payout failed", slog.Any("error", markErr))
		}
		return Payout{}, err
	}

	if err := s.store.MarkProcessing(ctx, p.ID, transfer.TransferCode); err != nil {
		return Payout{}, err
	}
	p.Status = StatusProcessing
	p.TransferCode = transfer.TransferCode
	return p, nil
}

func (s *Service) HandleTransfer(ctx context.Context, tx pgx.Tx, event string, t paystack.Transfer) error {
	p, err := s.store.ByReferenceTx(ctx, tx, t.Reference)
	if errors.Is(err, ErrNotFound) {
		s.log.WarnContext(ctx, "transfer event for unknown payout", slog.String("reference", t.Reference))
		return nil
	}
	if err != nil {
		return err
	}

	switch event {
	case "transfer.success":
		if p.Status == StatusPaid {
			return nil
		}
		if t.AmountMinor != p.AmountMinor {
			return fmt.Errorf("payout %s: paystack reports %d, we requested %d",
				p.Reference, t.AmountMinor, p.AmountMinor)
		}
		if _, err := ledger.PostTx(ctx, tx,
			ledger.Payout(p.HostID, p.Currency, p.Reference, p.AmountMinor)); err != nil {
			return err
		}
		if err := s.store.SetStatusTx(ctx, tx, p.ID, StatusPaid, ""); err != nil {
			return err
		}
		return s.notify(ctx, tx, p, notify.PayoutPaid(p.AmountMinor), "payout.paid:")

	case "transfer.failed":
		if p.Status == StatusPaid {
			return nil
		}
		reason := firstNonEmpty(t.FailureReason, t.Reason, "transfer failed")
		return s.store.SetStatusTx(ctx, tx, p.ID, StatusFailed, reason)

	case "transfer.reversed":
		if p.Status != StatusPaid {
			return s.store.SetStatusTx(ctx, tx, p.ID, StatusReversed, "reversed before settlement")
		}
		if _, err := ledger.PostTx(ctx, tx, ledger.PayoutReversed(
			p.HostID, p.Currency, p.Reference+"-rev", p.AmountMinor)); err != nil {
			return err
		}
		if err := s.store.SetStatusTx(ctx, tx, p.ID, StatusReversed, "reversed by the processor"); err != nil {
			return err
		}
		return s.notify(ctx, tx, p, notify.PayoutReversed(p.AmountMinor), "payout.reversed:")
	}
	return nil
}

// notify raises a payout notice inside the webhook transaction. The reversal
// case is the one that matters: without it, a host's balance silently goes back
// up and nobody tells them why.
func (s *Service) notify(ctx context.Context, tx pgx.Tx, p Payout, e notify.Event, keyPrefix string) error {
	if s.notifier == nil {
		return nil
	}
	e.UserID = p.HostID
	e.Key = keyPrefix + p.Reference
	e.Data = map[string]any{"payout_id": p.ID.String()}
	return s.notifier.NotifyTx(ctx, tx, e)
}

// Reconcile asks Paystack about payouts that have been processing too long.
// Webhooks are the primary path; this exists because a lost delivery otherwise
// leaves a host's money invisible to them forever.
func (s *Service) Reconcile(ctx context.Context, olderThan time.Duration, limit int32) (int, error) {
	stuck, err := s.store.Stuck(ctx, olderThan, limit)
	if err != nil {
		return 0, err
	}

	resolved := 0
	for _, p := range stuck {
		transfer, err := s.client.VerifyTransfer(ctx, p.Reference)
		if err != nil {
			s.log.WarnContext(ctx, "could not verify stuck payout",
				slog.String("reference", p.Reference), slog.Any("error", err))
			continue
		}

		event := ""
		switch transfer.Status {
		case "success":
			event = "transfer.success"
		case "failed", "abandoned":
			event = "transfer.failed"
		case "reversed":
			event = "transfer.reversed"
		default:
			continue // still genuinely in flight
		}

		err = postgres.InTx(ctx, s.store.Pool(), func(tx pgx.Tx) error {
			return s.HandleTransfer(ctx, tx, event, transfer)
		})
		if err != nil {
			s.log.ErrorContext(ctx, "reconciliation failed",
				slog.String("reference", p.Reference), slog.Any("error", err))
			continue
		}
		resolved++
	}
	return resolved, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
