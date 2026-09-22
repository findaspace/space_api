package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// InsertTx creates the notification and reports whether it is new. A replayed
// event returns false, and the caller must not enqueue a second delivery.
func (s *Store) InsertTx(ctx context.Context, tx pgx.Tx, e Event) (uuid.UUID, bool, error) {
	data, err := json.Marshal(e.Data)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("encode notification data: %w", err)
	}
	if e.Data == nil {
		data = []byte("{}")
	}

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO notifications (user_id, event, event_key, title, body, sms, data)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, event_key) DO NOTHING
		RETURNING id`,
		e.UserID, e.Kind, e.Key, e.Title, e.Body, e.SMS, data).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert notification: %w", err)
	}
	return id, true, nil
}

type Delivery struct {
	NotificationID uuid.UUID
	UserID         uuid.UUID
	Event          string
	SMS            string
}

func (s *Store) ForDelivery(ctx context.Context, id uuid.UUID) (Delivery, error) {
	var d Delivery
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, event, sms FROM notifications WHERE id = $1`, id).
		Scan(&d.NotificationID, &d.UserID, &d.Event, &d.SMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Delivery{}, ErrNotFound
	}
	if err != nil {
		return Delivery{}, fmt.Errorf("load notification: %w", err)
	}
	return d, nil
}

func (s *Store) WantsSMS(ctx context.Context, userID uuid.UUID, event string) (bool, error) {
	var want bool
	err := s.pool.QueryRow(ctx,
		`SELECT sms FROM notification_preferences WHERE user_id = $1 AND event = $2`,
		userID, event).Scan(&want)
	if errors.Is(err, pgx.ErrNoRows) {
		return SMSDefault(event), nil
	}
	if err != nil {
		return false, fmt.Errorf("load preference: %w", err)
	}
	return want, nil
}

// Claim takes ownership of one channel's delivery. It succeeds only from pending
// or failed. A row left in 'sending' by a crashed worker is ambiguous, so it is
// never claimed again: that is the at-most-once guarantee, and why a lost SMS is
// possible but a duplicate is not.
func (s *Store) Claim(ctx context.Context, notificationID uuid.UUID, channel string) (bool, error) {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO notification_deliveries (notification_id, channel)
		VALUES ($1, $2::notification_channel)
		ON CONFLICT (notification_id, channel) DO NOTHING`,
		notificationID, channel); err != nil {
		return false, fmt.Errorf("create delivery: %w", err)
	}

	tag, err := s.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET status = 'sending', attempts = attempts + 1
		WHERE notification_id = $1 AND channel = $2::notification_channel
		  AND status IN ('pending', 'failed')`,
		notificationID, channel)
	if err != nil {
		return false, fmt.Errorf("claim delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) Finish(ctx context.Context, notificationID uuid.UUID, channel, status, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE notification_deliveries
		SET status = $3::delivery_status, error = $4,
		    sent_at = CASE WHEN $3::delivery_status = 'sent' THEN now() ELSE sent_at END
		WHERE notification_id = $1 AND channel = $2::notification_channel`,
		notificationID, channel, status, errMsg)
	if err != nil {
		return fmt.Errorf("finish delivery: %w", err)
	}
	return nil
}

func (s *Store) Inbox(ctx context.Context, userID, before uuid.UUID, limit int32) ([]Notification, int, error) {
	var unread int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`,
		userID).Scan(&unread); err != nil {
		return nil, 0, fmt.Errorf("count unread: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, event, title, body, sms, data, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		  AND ($2::uuid = '00000000-0000-0000-0000-000000000000' OR id < $2)
		ORDER BY id DESC LIMIT $3`, userID, before, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()

	out := []Notification{}
	for rows.Next() {
		var (
			n    Notification
			data []byte
		)
		if err := rows.Scan(&n.ID, &n.UserID, &n.Event, &n.Title, &n.Body, &n.SMS,
			&data, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan notification: %w", err)
		}
		_ = json.Unmarshal(data, &n.Data)
		out = append(out, n)
	}
	return out, unread, rows.Err()
}

func (s *Store) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, now())
		 WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) MarkAllRead(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all read: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) SetPreference(ctx context.Context, userID uuid.UUID, event string, sms bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, event, sms)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, event) DO UPDATE SET sms = EXCLUDED.sms`,
		userID, event, sms)
	if err != nil {
		return fmt.Errorf("set preference: %w", err)
	}
	return nil
}
