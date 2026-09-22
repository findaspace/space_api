package notify

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/samnart1/space_api/internal/jobs"
	"github.com/samnart1/space_api/internal/platform/sms"
)

type PhoneSource interface {
	PhoneFor(ctx context.Context, userID uuid.UUID) (string, error)
}

// Service is shared infrastructure rather than a domain: every domain may
// depend on it, and it never imports a domain back, so it cannot form a cycle.
type Service struct {
	store  *Store
	queue  *jobs.Enqueuer
	sender sms.Sender
	phones PhoneSource
	log    *slog.Logger
}

func NewService(store *Store, queue *jobs.Enqueuer, sender sms.Sender,
	phones PhoneSource, log *slog.Logger) *Service {
	return &Service{store: store, queue: queue, sender: sender, phones: phones, log: log}
}

// NotifyTx records a notification and schedules its delivery inside the
// caller's transaction. If the caller rolls back, neither exists.
func (s *Service) NotifyTx(ctx context.Context, tx pgx.Tx, e Event) error {
	if !KnownEvent(e.Kind) {
		return ErrUnknownEvent
	}

	id, fresh, err := s.store.InsertTx(ctx, tx, e)
	if err != nil {
		return err
	}
	if !fresh {
		return nil // already raised; delivering again would duplicate it
	}

	return s.queue.InsertTxOpts(ctx, tx,
		jobs.DeliverNotificationArgs{NotificationID: id},
		&river.InsertOpts{MaxAttempts: 5})
}

// Deliver runs in the worker. The in-app record already exists; this only
// pushes it out to the channels the user wants.
func (s *Service) Deliver(ctx context.Context, notificationID uuid.UUID) error {
	d, err := s.store.ForDelivery(ctx, notificationID)
	if err != nil {
		return err
	}
	if d.SMS == "" {
		return nil
	}

	want, err := s.store.WantsSMS(ctx, d.UserID, d.Event)
	if err != nil {
		return err
	}
	if !want {
		return nil
	}

	claimed, err := s.store.Claim(ctx, notificationID, "sms")
	if err != nil {
		return err
	}
	if !claimed {
		return nil // sent already, or ambiguously in flight: never resend
	}

	phone, err := s.phones.PhoneFor(ctx, d.UserID)
	if err != nil {
		_ = s.store.Finish(ctx, notificationID, "sms", "failed", err.Error())
		return err
	}

	if err := s.sender.Send(ctx, phone, d.SMS); err != nil {
		// 'failed' is claimable again, so a River retry is a real second
		// attempt. Only 'sending' is the ambiguous state that blocks resends.
		_ = s.store.Finish(ctx, notificationID, "sms", "failed", err.Error())
		return err
	}
	return s.store.Finish(ctx, notificationID, "sms", "sent", "")
}

func (s *Service) Inbox(ctx context.Context, userID, before uuid.UUID, limit int32) ([]Notification, int, error) {
	return s.store.Inbox(ctx, userID, before, limit)
}

func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	return s.store.MarkRead(ctx, userID, id)
}

func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error) {
	return s.store.MarkAllRead(ctx, userID)
}

func (s *Service) SetPreference(ctx context.Context, userID uuid.UUID, event string, sms bool) error {
	if !KnownEvent(event) {
		return ErrUnknownEvent
	}
	return s.store.SetPreference(ctx, userID, event, sms)
}
