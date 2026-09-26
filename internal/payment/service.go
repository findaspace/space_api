package payment

import (
	"context"
	"encoding/json"
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

type PayerSource interface {
	ContactFor(ctx context.Context, userID uuid.UUID) (email, phone string, err error)
}

// TransferHandler receives transfer events. Declared here because webhook
// verification and deduplication live in this package; payouts plug into it.
type TransferHandler interface {
	HandleTransfer(ctx context.Context, tx pgx.Tx, event string, t paystack.Transfer) error
}

type Notifier interface {
	NotifyTx(ctx context.Context, tx pgx.Tx, e notify.Event) error
}

type ServiceConfig struct {
	Secret      string
	CallbackURL string
	Channels    []string
}

type Service struct {
	store     *Store
	client    *paystack.Client
	bookings  BookingGateway
	payers    PayerSource
	transfers TransferHandler
	notifier  Notifier
	log       *slog.Logger
	cfg       ServiceConfig
}

func NewService(store *Store, client *paystack.Client, bookings BookingGateway,
	payers PayerSource, log *slog.Logger, cfg ServiceConfig) *Service {
	return &Service{store: store, client: client, bookings: bookings,
		payers: payers, log: log, cfg: cfg}
}

// The two setters exist because payouts and notifications both depend on
// packages that depend back on this one's webhook path. One side of each pair
// has to be wired late; doing it explicitly beats a shared package.
func (s *Service) SetTransferHandler(h TransferHandler) { s.transfers = h }
func (s *Service) SetNotifier(n Notifier)               { s.notifier = n }

// Intent starts a payment for a booking and returns where to send the guest.
func (s *Service) Intent(ctx context.Context, bookingID, guestID uuid.UUID) (Payment, error) {
	snap, err := s.bookings.Snapshot(ctx, bookingID)
	if err != nil {
		return Payment{}, err
	}
	if snap.GuestID != guestID {
		return Payment{}, ErrNotFound
	}
	if !Payable(snap.Mode, snap.Status) {
		return Payment{}, ErrNotPayable
	}

	if existing, err := s.store.OpenForBooking(ctx, bookingID); err == nil {
		return existing, nil // resume rather than start a second attempt
	}

	email, phone, err := s.payers.ContactFor(ctx, guestID)
	if err != nil {
		return Payment{}, err
	}
	// Paystack requires an email; this product identifies people by phone.
	if email == "" {
		email = strings.TrimPrefix(phone, "+") + "@guest.space.gh"
	}

	ref, err := paystack.NewReference()
	if err != nil {
		return Payment{}, err
	}

	init, err := s.client.Initialize(ctx, paystack.InitializeRequest{
		Email:       email,
		AmountMinor: snap.DueNowMinor,
		Currency:    snap.Currency,
		Reference:   ref,
		// CallbackURL: s.cfg.CallbackURL,
		CallbackURL: strings.ReplaceAll(s.cfg.CallbackURL, "{booking_id}", snap.ID.String()),
		Channels:    s.cfg.Channels,
		Metadata: map[string]any{
			"booking_id":        snap.ID.String(),
			"booking_reference": snap.Reference,
		},
	})
	if err != nil {
		return Payment{}, err
	}

	return s.store.Create(ctx, Payment{
		BookingID:        bookingID,
		PayerID:          guestID,
		ProviderRef:      ref,
		AccessCode:       init.AccessCode,
		AuthorizationURL: init.AuthorizationURL,
		Currency:         snap.Currency,
		AmountMinor:      snap.DueNowMinor,
	})
}

// HandleWebhook verifies, deduplicates and applies one Paystack event. Every
// effect commits together or not at all, so a retry is always safe.
func (s *Service) HandleWebhook(ctx context.Context, rawBody []byte, signature string) error {
	if !paystack.VerifySignature(s.cfg.Secret, rawBody, signature) {
		return ErrBadSignature
	}

	event, err := paystack.ParseEvent(rawBody)
	if err != nil {
		return err
	}

	// Charges and transfers both carry a stable numeric id; pull it without
	// committing to either shape.
	var idOnly struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(event.Data, &idOnly)
	key := fmt.Sprintf("%s:%d", event.Event, idOnly.ID)

	return postgres.InTx(ctx, s.store.Pool(), func(tx pgx.Tx) error {
		fresh, err := s.store.RecordEventTx(ctx, tx, key, event.Event, rawBody)
		if err != nil {
			return err
		}
		if !fresh {
			s.log.InfoContext(ctx, "webhook already processed", slog.String("event_key", key))
			return nil
		}

		switch event.Event {
		case "charge.success", "charge.failed":
			txn, err := event.Transaction()
			if err != nil {
				return err
			}
			if event.Event == "charge.success" {
				if err := s.applyChargeSuccess(ctx, tx, txn); err != nil {
					return err
				}
			} else if err := s.applyChargeFailed(ctx, tx, txn); err != nil {
				return err
			}

		case "transfer.success", "transfer.failed", "transfer.reversed":
			if s.transfers == nil {
				s.log.WarnContext(ctx, "no transfer handler registered")
				break
			}
			tr, err := event.Transfer()
			if err != nil {
				return err
			}
			if err := s.transfers.HandleTransfer(ctx, tx, event.Event, tr); err != nil {
				return err
			}

		default:
			s.log.InfoContext(ctx, "ignoring webhook event", slog.String("event", event.Event))
		}

		return s.store.MarkEventProcessedTx(ctx, tx, key)
	})
}

func (s *Service) applyChargeSuccess(ctx context.Context, tx pgx.Tx, txn paystack.Transaction) error {
	p, err := s.store.ByRefTx(ctx, tx, txn.Reference)
	if err != nil {
		return err
	}
	if p.Status == StatusSucceeded {
		return nil
	}

	// Never trust the event for what is owed. Compare against what we sent.
	if txn.AmountMinor != p.AmountMinor || !strings.EqualFold(txn.Currency, p.Currency) {
		s.log.ErrorContext(ctx, "payment amount mismatch",
			slog.String("reference", p.ProviderRef),
			slog.Int64("expected", p.AmountMinor),
			slog.Int64("received", txn.AmountMinor))
		if err := s.store.MarkFailedTx(ctx, tx, p.ID, "amount mismatch"); err != nil {
			return err
		}
		return ErrAmountMismatch
	}

	snap, err := s.bookings.Snapshot(ctx, p.BookingID)
	if err != nil {
		return err
	}

	paidAt := time.Now()
	if txn.PaidAt != nil {
		paidAt = *txn.PaidAt
	}
	if err := s.store.MarkSucceededTx(ctx, tx, p.ID,
		fmt.Sprint(txn.ID), txn.Channel, txn.FeesMinor, paidAt); err != nil {
		return err
	}

	// The platform fee is revenue; the rest becomes a liability to the host.
	netMinor := p.AmountMinor - snap.FeesMinor
	if _, err := ledger.PostTx(ctx, tx, ledger.Charge(
		&p.BookingID, snap.HostID, p.Currency, p.ProviderRef, netMinor, snap.FeesMinor,
	)); err != nil {
		return err
	}

	if err := s.bookings.MarkPaid(ctx, tx, p.BookingID, snap.Status); err != nil {
		return err
	}

	return s.notifyConfirmed(ctx, tx, snap, p.AmountMinor)
}

// notifyConfirmed raises both sides' notices inside the webhook transaction, so
// a confirmed booking can never exist without them and a rolled-back payment
// leaves no ghost notice behind.
func (s *Service) notifyConfirmed(ctx context.Context, tx pgx.Tx, snap Snapshot, paidMinor int64) error {
	if s.notifier == nil {
		return nil
	}
	data := map[string]any{"booking_id": snap.ID.String()}

	guest := notify.BookingConfirmedGuest(snap.Reference, snap.StartsOn, paidMinor)
	guest.UserID = snap.GuestID
	guest.Key = "booking.confirmed:" + snap.ID.String() + ":guest"
	guest.Data = data
	if err := s.notifier.NotifyTx(ctx, tx, guest); err != nil {
		return err
	}

	host := notify.BookingConfirmedHost(snap.Reference, snap.StartsOn)
	host.UserID = snap.HostID
	host.Key = "booking.confirmed:" + snap.ID.String() + ":host"
	host.Data = data
	return s.notifier.NotifyTx(ctx, tx, host)
}

func (s *Service) applyChargeFailed(ctx context.Context, tx pgx.Tx, txn paystack.Transaction) error {
	p, err := s.store.ByRefTx(ctx, tx, txn.Reference)
	if err != nil {
		return err
	}
	if p.Status == StatusSucceeded {
		return nil // a failure after a success is noise, never a reversal
	}
	reason := txn.GatewayResponse
	if reason == "" {
		reason = "payment failed"
	}
	return s.store.MarkFailedTx(ctx, tx, p.ID, reason)
}
