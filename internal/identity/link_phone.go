package identity

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

func (s *Service) LinkPhone(ctx context.Context, userID uuid.UUID, raw, code string) (User, error) {
	phone, err := NormalizePhone(raw)
	if err != nil {
		return User{}, err
	}
	ch, err := s.store.ClaimChallenge(ctx, phone)
	if err != nil {
		return User{}, err
	}
	if s.now().After(ch.ExpiresAt) {
		return User{}, ErrOTPExpired
	}
	if ch.Attempts > ch.MaxAttempts {
		return User{}, ErrOTPExhausted
	}
	if !s.hasher.Match(phone, code, ch.CodeHash) {
		return User{}, ErrOTPMismatch
	}
	err = postgres.InTx(ctx, s.store.pool, func(tx pgx.Tx) error {
		user, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", userID))
		if err != nil {
			return err
		}
		if user.Status != StatusActive {
			return ErrUserSuspended
		}
		if user.Phone != "" && user.Phone != phone {
			return ErrIdentityConflict
		}
		tag, err := tx.Exec(ctx, "UPDATE otp_challenges SET consumed_at=now() WHERE id=$1 AND consumed_at IS NULL AND phone_e164=$2", ch.ID, phone)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrOTPNotFound
		}
		_, err = tx.Exec(ctx, "UPDATE users SET phone_e164=$2,phone_verified_at=now() WHERE id=$1", userID, phone)
		var p *pgconn.PgError
		if errors.As(err, &p) && p.Code == "23505" {
			return ErrIdentityConflict
		}
		return err
	})
	if err != nil {
		return User{}, err
	}
	return s.Me(ctx, userID)
}
