package payout

import (
	"context"
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

const accountColumns = `id, user_id, recipient_code, channel::text, bank_code,
	account_number, account_name, currency, verified_at, is_default, created_at`

func scanAccount(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.UserID, &a.RecipientCode, &a.Channel, &a.BankCode,
		&a.AccountNumber, &a.AccountName, &a.Currency, &a.VerifiedAt,
		&a.IsDefault, &a.CreatedAt)
	return a, err
}

// SaveAccount adds or replaces the host's default payout destination. Making
// one default demotes the rest in the same transaction, since the partial
// unique index would otherwise reject the insert.
func (s *Store) SaveAccount(ctx context.Context, a Account) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx,
		`UPDATE payout_accounts SET is_default = false WHERE user_id = $1 AND is_default`,
		a.UserID); err != nil {
		return Account{}, fmt.Errorf("demote existing accounts: %w", err)
	}

	out, err := scanAccount(tx.QueryRow(ctx, `
		INSERT INTO payout_accounts (user_id, channel, bank_code, account_number,
			account_name, currency, recipient_code, is_default)
		VALUES ($1, $2::payout_channel, $3, $4, $5, $6, $7, true)
		ON CONFLICT (user_id, channel, bank_code, account_number) DO UPDATE
			SET account_name = EXCLUDED.account_name,
			    recipient_code = COALESCE(NULLIF(EXCLUDED.recipient_code, ''), payout_accounts.recipient_code),
			    is_default = true
		RETURNING `+accountColumns,
		a.UserID, string(a.Channel), a.BankCode, a.AccountNumber,
		a.AccountName, a.Currency, a.RecipientCode))
	if err != nil {
		return Account{}, fmt.Errorf("save payout account: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}

func (s *Store) DefaultAccount(ctx context.Context, userID uuid.UUID) (Account, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM payout_accounts
		 WHERE user_id = $1 AND is_default`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	if err != nil {
		return Account{}, fmt.Errorf("get payout account: %w", err)
	}
	return a, nil
}

func (s *Store) SetRecipientCode(ctx context.Context, accountID uuid.UUID, code string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE payout_accounts SET recipient_code = $2, verified_at = now() WHERE id = $1`,
		accountID, code)
	if err != nil {
		return fmt.Errorf("store recipient code: %w", err)
	}
	return nil
}

// Balance derives everything from the ledger. Credits count only once their
// booking has matured; debits count immediately, because a refund clawback or
// a payout already taken must never inflate what a host can withdraw.
func (s *Store) Balance(ctx context.Context, hostID uuid.UUID, holdDays int) (Balance, error) {
	b := Balance{Currency: "GHS"}

	const q = `
		WITH host_entries AS (
			SELECT e.direction, e.amount_minor, b.starts_on
			FROM ledger_entries e
			JOIN ledger_transactions t ON t.id = e.transaction_id
			LEFT JOIN bookings b ON b.id = t.booking_id
			WHERE e.party_id = $1 AND e.account = 'host_payable'
		)
		SELECT
			COALESCE(SUM(CASE WHEN direction = 'credit' THEN amount_minor ELSE -amount_minor END), 0),
			COALESCE(SUM(CASE
				WHEN direction = 'debit' THEN -amount_minor
				WHEN starts_on IS NULL OR starts_on + $2::int <= CURRENT_DATE THEN amount_minor
				ELSE 0
			END), 0)
		FROM host_entries`

	if err := s.pool.QueryRow(ctx, q, hostID, holdDays).
		Scan(&b.TotalMinor, &b.AvailableMinor); err != nil {
		return Balance{}, fmt.Errorf("compute host balance: %w", err)
	}

	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_minor), 0) FROM payouts
		WHERE host_id = $1 AND status IN ('pending','processing')`, hostID).
		Scan(&b.InFlightMinor); err != nil {
		return Balance{}, fmt.Errorf("compute in-flight payouts: %w", err)
	}

	b.PendingMinor = b.TotalMinor - b.AvailableMinor

	// A payout that has been requested but not yet settled has no ledger entry
	// until it succeeds, so it is subtracted here to stop a second withdrawal
	// against the same money.
	b.WithdrawableMinor = b.AvailableMinor - b.InFlightMinor
	if b.WithdrawableMinor < 0 {
		b.WithdrawableMinor = 0
	}
	return b, nil
}

const payoutColumns = `id, host_id, account_id, reference, transfer_code,
	status::text, currency, amount_minor, failure_reason, settled_at, created_at`

func scanPayout(row pgx.Row) (Payout, error) {
	var p Payout
	err := row.Scan(&p.ID, &p.HostID, &p.AccountID, &p.Reference, &p.TransferCode,
		&p.Status, &p.Currency, &p.AmountMinor, &p.FailureReason,
		&p.SettledAt, &p.CreatedAt)
	return p, err
}

func (s *Store) CreateRequest(ctx context.Context, hostID, accountID uuid.UUID, currency string, amountMinor int64) (Payout, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Payout{}, fmt.Errorf("generate payout id: %w", err)
	}

	p, err := scanPayout(s.pool.QueryRow(ctx, `
		INSERT INTO payouts (id, host_id, account_id, reference, currency, amount_minor)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+payoutColumns,
		id, hostID, accountID, NewReference(id), currency, amountMinor))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "payouts_one_open_per_host" {
			return Payout{}, ErrInProgress
		}
		return Payout{}, fmt.Errorf("create payout: %w", err)
	}
	return p, nil
}

func (s *Store) ByReference(ctx context.Context, ref string) (Payout, error) {
	p, err := scanPayout(s.pool.QueryRow(ctx,
		`SELECT `+payoutColumns+` FROM payouts WHERE reference = $1`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payout{}, ErrNotFound
	}
	if err != nil {
		return Payout{}, fmt.Errorf("get payout: %w", err)
	}
	return p, nil
}

func (s *Store) ListForHost(ctx context.Context, hostID uuid.UUID, limit, offset int32) ([]Payout, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+payoutColumns+` FROM payouts WHERE host_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, hostID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list payouts: %w", err)
	}
	defer rows.Close()

	out := []Payout{}
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, fmt.Errorf("scan payout: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) MarkProcessing(ctx context.Context, id uuid.UUID, transferCode string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE payouts SET status = 'processing', transfer_code = $2
		 WHERE id = $1 AND status = 'pending'`, id, transferCode)
	if err != nil {
		return fmt.Errorf("mark payout processing: %w", err)
	}
	return nil
}

func (s *Store) MarkFailed(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE payouts SET status = 'failed', failure_reason = $2
		 WHERE id = $1 AND status IN ('pending','processing')`, id, reason)
	if err != nil {
		return fmt.Errorf("mark payout failed: %w", err)
	}
	return nil
}

// ByReferenceTx locks the row so two deliveries of the same event serialize.
func (s *Store) ByReferenceTx(ctx context.Context, tx pgx.Tx, ref string) (Payout, error) {
	p, err := scanPayout(tx.QueryRow(ctx,
		`SELECT `+payoutColumns+` FROM payouts WHERE reference = $1 FOR UPDATE`, ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return Payout{}, ErrNotFound
	}
	if err != nil {
		return Payout{}, fmt.Errorf("get payout: %w", err)
	}
	return p, nil
}

func (s *Store) SetStatusTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, status Status, reason string) error {
	_, err := tx.Exec(ctx, `
		UPDATE payouts SET status = $2::payout_status, failure_reason = $3,
			settled_at = CASE WHEN $2::payout_status = 'paid' THEN COALESCE(settled_at, now()) ELSE settled_at END
		WHERE id = $1`, id, string(status), reason)
	if err != nil {
		return fmt.Errorf("set payout status: %w", err)
	}
	return nil
}

// Stuck returns payouts that have been processing long enough to be suspicious.
// Paystack retries internally, so a few minutes is normal and an hour is not.
func (s *Store) Stuck(ctx context.Context, olderThan time.Duration, limit int32) ([]Payout, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+payoutColumns+` FROM payouts
		 WHERE status = 'processing' AND updated_at < now() - $1::interval
		 ORDER BY updated_at LIMIT $2`, olderThan, limit)
	if err != nil {
		return nil, fmt.Errorf("list stuck payouts: %w", err)
	}
	defer rows.Close()

	out := []Payout{}
	for rows.Next() {
		p, err := scanPayout(rows)
		if err != nil {
			return nil, fmt.Errorf("scan payout: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
