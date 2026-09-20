package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Post writes a balanced posting. Callers that already hold a transaction pass
// it in, so a charge and the booking status change it causes commit together.
func (s *Store) Post(ctx context.Context, p Posting) (uuid.UUID, error) {
	var id uuid.UUID
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, err = PostTx(ctx, tx, p)
		return err
	})
	return id, err
}

// PostTx writes a posting inside an existing transaction.
func PostTx(ctx context.Context, tx pgx.Tx, p Posting) (uuid.UUID, error) {
	if err := p.Validate(); err != nil {
		return uuid.Nil, err
	}

	var ref *string
	if p.ExternalRef != "" {
		ref = &p.ExternalRef
	}

	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO ledger_transactions (kind, booking_id, currency, memo, external_ref)
		VALUES ($1::ledger_kind, $2, $3, $4, $5)
		RETURNING id`,
		string(p.Kind), p.BookingID, p.Currency, p.Memo, ref).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.ConstraintName == "ledger_transactions_external_idx" {
			return uuid.Nil, ErrDuplicate
		}
		return uuid.Nil, fmt.Errorf("create ledger transaction: %w", err)
	}

	rows := make([][]any, 0, len(p.Entries))
	for _, e := range p.Entries {
		rows = append(rows, []any{id, string(e.Account), string(e.Direction), e.Amount, e.PartyID})
	}

	_, err = tx.CopyFrom(ctx,
		pgx.Identifier{"ledger_entries"},
		[]string{"transaction_id", "account", "direction", "amount_minor", "party_id"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return uuid.Nil, fmt.Errorf("insert ledger entries: %w", err)
	}

	// The deferred balance trigger fires at COMMIT, not here. An unbalanced
	// posting fails the whole transaction rather than this statement.
	return id, nil
}

// Balance sums one account, optionally for one party. Assets are debit-positive
// and liabilities credit-positive, so the sign convention is applied per account
// rather than left to the caller.
func (s *Store) Balance(ctx context.Context, account Account, partyID *uuid.UUID) (int64, error) {
	sign := "CASE WHEN direction = 'debit' THEN amount_minor ELSE -amount_minor END"
	if isCreditNormal(account) {
		sign = "CASE WHEN direction = 'credit' THEN amount_minor ELSE -amount_minor END"
	}

	var balance int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(`+sign+`), 0) FROM ledger_entries
		WHERE account = $1::ledger_account
		  AND ($2::uuid IS NULL OR party_id = $2)`,
		string(account), partyID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("compute balance: %w", err)
	}
	return balance, nil
}

func isCreditNormal(a Account) bool {
	switch a {
	case AccountHostPayable, AccountPlatformRevenue, AccountRefundsPayable:
		return true
	}
	return false
}

// TrialBalance proves the whole ledger sums to zero. Run it in a scheduled job
// and alert on any non-zero result: it means something bypassed Post.
func (s *Store) TrialBalance(ctx context.Context) (int64, error) {
	var net int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(
			CASE WHEN direction = 'debit' THEN amount_minor ELSE -amount_minor END
		), 0) FROM ledger_entries`).Scan(&net)
	if err != nil {
		return 0, fmt.Errorf("trial balance: %w", err)
	}
	return net, nil
}
