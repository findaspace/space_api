package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Pool() *pgxpool.Pool { return s.pool }

const columns = `id, booking_id, payer_id, provider_ref, access_code,
	authorization_url, channel, status::text, currency, amount_minor, paid_at, created_at`

func scan(row pgx.Row) (Payment, error) {
	var p Payment
	err := row.Scan(&p.ID, &p.BookingID, &p.PayerID, &p.ProviderRef, &p.AccessCode,
		&p.AuthorizationURL, &p.Channel, &p.Status, &p.Currency, &p.AmountMinor,
		&p.PaidAt, &p.CreatedAt)
	return p, err
}

func (s *Store) Create(ctx context.Context, p Payment) (Payment, error) {
	out, err := scan(s.pool.QueryRow(ctx, `
		INSERT INTO payments (booking_id, payer_id, provider_ref, access_code,
			authorization_url, currency, amount_minor)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+columns,
		p.BookingID, p.PayerID, p.ProviderRef, p.AccessCode,
		p.AuthorizationURL, p.Currency, p.AmountMinor))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.ConstraintName {
			case "payments_one_success_per_booking":
				return Payment{}, ErrAlreadyPaid
			case "payments_one_open_per_booking":
				// An attempt is already in flight; hand back that one.
				return s.OpenForBooking(ctx, p.BookingID)
			}
		}
		return Payment{}, fmt.Errorf("create payment: %w", err)
	}
	return out, nil
}

func (s *Store) OpenForBooking(ctx context.Context, bookingID uuid.UUID) (Payment, error) {
	p, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM payments
		 WHERE booking_id = $1 AND status IN ('pending','processing')`, bookingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("get open payment: %w", err)
	}
	return p, nil
}

func (s *Store) ByRefTx(ctx context.Context, tx pgx.Tx, ref string) (Payment, error) {
	// FOR UPDATE: two deliveries of the same event must serialize here.
	p, err := scan(tx.QueryRow(ctx,
		`SELECT `+columns+` FROM payments WHERE provider_ref = $1 FOR UPDATE`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	if err != nil {
		return Payment{}, fmt.Errorf("get payment by ref: %w", err)
	}
	return p, nil
}

func (s *Store) MarkSucceededTx(ctx context.Context, tx pgx.Tx, id uuid.UUID,
	txnID, channel string, feeMinor int64, paidAt time.Time) error {

	_, err := tx.Exec(ctx, `
		UPDATE payments SET status = 'succeeded', provider_txn_id = $2,
			channel = $3, provider_fee_minor = $4, paid_at = $5
		WHERE id = $1`, id, txnID, channel, feeMinor, paidAt)
	if err != nil {
		return fmt.Errorf("mark payment succeeded: %w", err)
	}
	return nil
}

func (s *Store) MarkFailedTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error {
	_, err := tx.Exec(ctx,
		`UPDATE payments SET status = 'failed', failure_reason = $2 WHERE id = $1`, id, reason)
	if err != nil {
		return fmt.Errorf("mark payment failed: %w", err)
	}
	return nil
}

// RecordEventTx inserts the event and returns false if it was already seen.
// Paystack retries on any non-200, so duplicates are the normal case.
func (s *Store) RecordEventTx(ctx context.Context, tx pgx.Tx, key, eventType string, payload []byte) (bool, error) {
	if !json.Valid(payload) {
		return false, fmt.Errorf("webhook payload is not valid json")
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO webhook_events (provider, event_key, event_type, payload)
		VALUES ('paystack', $1, $2, $3)
		ON CONFLICT (provider, event_key) DO NOTHING`, key, eventType, payload)
	if err != nil {
		return false, fmt.Errorf("record webhook event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) MarkEventProcessedTx(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx,
		`UPDATE webhook_events SET processed_at = now()
		 WHERE provider = 'paystack' AND event_key = $1`, key)
	if err != nil {
		return fmt.Errorf("mark event processed: %w", err)
	}
	return nil
}
