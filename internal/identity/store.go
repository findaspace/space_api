package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/platform/postgres"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

type Challenge struct {
	ID          uuid.UUID
	Phone       string
	CodeHash    []byte
	Attempts    int16
	MaxAttempts int16
	ExpiresAt   time.Time
}

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	FamilyID  uuid.UUID
	ExpiresAt time.Time
}

type RateLimitedError struct{ RetryAfter time.Duration }

type UserSummary struct {
	DisplayName     string
	CreatedAt       time.Time
	PhoneVerifiedAt *time.Time
}

func (s *Store) Summaries(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]UserSummary, error) {
	out := make(map[uuid.UUID]UserSummary, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT id, display_name, created_at, phone_verified_at FROM users WHERE id = ANY($1::uuid[])`, ids)
	if err != nil {
		return nil, fmt.Errorf("user summaries: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var u UserSummary
		if err := rows.Scan(&id, &u.DisplayName, &u.CreatedAt, &u.PhoneVerifiedAt); err != nil {
			return nil, fmt.Errorf("scan user summary: %w", err)
		}
		out[id] = u
	}
	return out, rows.Err()
}

func (e *RateLimitedError) Error() string        { return "identity: too many requests" }
func (e *RateLimitedError) Is(target error) bool { return target == ErrTooManyOTP }

const userColumns = `id, phone_e164, email, display_name, status::text, phone_verified_at, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Phone, &u.Email, &u.DisplayName, &u.Status, &u.PhoneVerifiedAt, &u.CreatedAt)
	return u, err
}

// CountRecent powers the OTP send limit. Both counters come from one scan of
// the recent window rather than two round trips.
func (s *Store) CountRecent(ctx context.Context, phone, ip string, window time.Duration) (byPhone, byIP int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE phone_e164 = $1),
			count(*) FILTER (WHERE request_ip = $2 AND $2 <> '')
		FROM otp_challenges
		WHERE created_at > now() - $3::interval
		  AND (phone_e164 = $1 OR ($2 <> '' AND request_ip = $2))`,
		phone, ip, window,
	).Scan(&byPhone, &byIP)
	if err != nil {
		return 0, 0, fmt.Errorf("count recent challenges: %w", err)
	}
	return byPhone, byIP, nil
}

func (s *Store) CreateChallenge(ctx context.Context, phone, ip string, codeHash []byte, ttl time.Duration, maxAttempts int16) (Challenge, error) {
	ch := Challenge{Phone: phone, CodeHash: codeHash, MaxAttempts: maxAttempts}

	err := s.pool.QueryRow(ctx, `
		INSERT INTO otp_challenges (phone_e164, code_hash, max_attempts, expires_at, request_ip)
		VALUES ($1, $2, $3, now() + $4::interval, $5)
		RETURNING id, expires_at`,
		phone, codeHash, maxAttempts, ttl, ip,
	).Scan(&ch.ID, &ch.ExpiresAt)
	if err != nil {
		return Challenge{}, fmt.Errorf("create challenge: %w", err)
	}
	return ch, nil
}

// ClaimChallenge locks the newest unconsumed challenge and increments its
// attempt counter, then commits. The comparison happens in Go afterwards.
//
// This ordering is the whole point: if the counter were incremented inside the
// transaction that also verifies the code, a wrong code would roll the
// increment back and the attempt limit would never trigger.
func (s *Store) ClaimChallenge(ctx context.Context, phone string) (Challenge, error) {
	ch := Challenge{Phone: phone}

	err := s.pool.QueryRow(ctx, `
		UPDATE otp_challenges SET attempts = attempts + 1
		WHERE id = (
			SELECT id FROM otp_challenges
			WHERE phone_e164 = $1 AND consumed_at IS NULL
			ORDER BY created_at DESC
			LIMIT 1
			FOR UPDATE
		)
		RETURNING id, code_hash, attempts, max_attempts, expires_at`,
		phone,
	).Scan(&ch.ID, &ch.CodeHash, &ch.Attempts, &ch.MaxAttempts, &ch.ExpiresAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, ErrOTPNotFound
	}
	if err != nil {
		return Challenge{}, fmt.Errorf("claim challenge: %w", err)
	}
	return ch, nil
}

// Login consumes the challenge, creates or updates the user, and opens a new
// session family. All of it or none of it.
func (s *Store) Login(ctx context.Context, challengeID uuid.UUID, phone, userAgent, ip string, refreshHash []byte, refreshTTL time.Duration) (User, Session, error) {
	var (
		user User
		sess Session
	)

	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE otp_challenges SET consumed_at = now() WHERE id = $1 AND consumed_at IS NULL`,
			challengeID)
		if err != nil {
			return fmt.Errorf("consume challenge: %w", err)
		}
		if tag.RowsAffected() != 1 {
			// Another request consumed it between our claim and now.
			return ErrOTPNotFound
		}

		user, err = scanUser(tx.QueryRow(ctx, `
			INSERT INTO users (phone_e164, phone_verified_at)
			VALUES ($1, now())
			ON CONFLICT (phone_e164) DO UPDATE
				SET phone_verified_at = COALESCE(users.phone_verified_at, now())
			RETURNING `+userColumns, phone))
		if err != nil {
			return fmt.Errorf("upsert user: %w", err)
		}
		if user.Status != StatusActive {
			return ErrUserSuspended
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO role_grants (user_id, role) VALUES ($1, 'guest'::user_role)
			 ON CONFLICT DO NOTHING`, user.ID); err != nil {
			return fmt.Errorf("grant guest role: %w", err)
		}

		user.Roles, err = loadRoles(ctx, tx, user.ID)
		if err != nil {
			return err
		}

		sess, err = insertSession(ctx, tx, user.ID, uuid.New(), refreshHash, userAgent, ip, refreshTTL, 0)
		return err
	})
	if err != nil {
		return User{}, Session{}, err
	}
	return user, sess, nil
}

// Rotate exchanges a refresh token for a new one.
//
// A token that was already rotated is still honoured for a short grace
// window, and only replay after that window counts as theft. Without it, two
// requests that refresh at the same moment look identical to a stolen token
// being replayed: the second revokes the whole family and signs out a user
// who did nothing wrong. space_web fires several requests in parallel on
// every navigation, so for it this is routine, not an edge case. Auth0 makes
// the same trade and calls it a reuse interval.
//
// COALESCE keeps the first rotation time, so using the token repeatedly
// inside the window cannot keep sliding it forward.
func (s *Store) Rotate(ctx context.Context, oldHash, newHash []byte, userAgent, ip string,
	refreshTTL, maxAge, grace time.Duration) (User, Session, error) {
	var (
		user User
		sess Session
	)

	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var (
			oldID    uuid.UUID
			userID   uuid.UUID
			familyID uuid.UUID
		)
		err := tx.QueryRow(ctx, `
			UPDATE sessions SET rotated_at = COALESCE(rotated_at, now())
			WHERE refresh_hash = $1
			  AND revoked_at IS NULL
			  AND expires_at > now()
			  AND (rotated_at IS NULL OR rotated_at > now() - $2::interval)
			RETURNING id, user_id, family_id`, oldHash, grace).Scan(&oldID, &userID, &familyID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionInvalid
		}
		if err != nil {
			return fmt.Errorf("claim session: %w", err)
		}

		user, err = scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
		if err != nil {
			return fmt.Errorf("load user: %w", err)
		}
		if user.Status != StatusActive {
			return ErrUserSuspended
		}

		user.Roles, err = loadRoles(ctx, tx, user.ID)
		if err != nil {
			return err
		}

		sess, err = insertSession(ctx, tx, user.ID, familyID, newHash, userAgent, ip, refreshTTL, maxAge)
		return err
	})
	if err != nil {
		return User{}, Session{}, err
	}
	return user, sess, nil
}

// DiagnoseRefresh runs only after Rotate reports ErrSessionInvalid. A token
// that exists but was already rotated means the client's stored copy leaked:
// somebody replayed it. Killing the family logs out the thief and the victim,
// which is the correct trade.
func (s *Store) DiagnoseRefresh(ctx context.Context, hash []byte) error {
	var (
		familyID uuid.UUID
		rotated  bool
		revoked  bool
	)
	err := s.pool.QueryRow(ctx, `
		SELECT family_id, rotated_at IS NOT NULL, revoked_at IS NOT NULL
		FROM sessions WHERE refresh_hash = $1`, hash).Scan(&familyID, &rotated, &revoked)

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSessionInvalid
	}
	if err != nil {
		return fmt.Errorf("diagnose refresh: %w", err)
	}

	// Already revoked, never rotated: collateral from an earlier revocation, or
	// a logout. Deny it, but this is not a fresh theft signal.
	if revoked && !rotated {
		return ErrSessionInvalid
	}
	if !rotated {
		return ErrSessionInvalid // merely expired
	}
	if err := s.RevokeFamily(ctx, familyID, "refresh token reuse"); err != nil {
		return err
	}
	return ErrSessionReuse
}

func (s *Store) RevokeFamily(ctx context.Context, familyID uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE family_id = $1 AND revoked_at IS NULL`, familyID, reason)
	if err != nil {
		return fmt.Errorf("revoke family: %w", err)
	}
	return nil
}

func (s *Store) RevokeByRefreshHash(ctx context.Context, hash []byte, reason string) error {
	var familyID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT family_id FROM sessions WHERE refresh_hash = $1`, hash).Scan(&familyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // logging out an unknown token is not an error
	}
	if err != nil {
		return fmt.Errorf("lookup session: %w", err)
	}
	return s.RevokeFamily(ctx, familyID, reason)
}

func (s *Store) UserByID(ctx context.Context, id uuid.UUID) (User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrSessionInvalid
	}
	if err != nil {
		return User{}, fmt.Errorf("load user: %w", err)
	}
	user.Roles, err = loadRoles(ctx, s.pool, user.ID)
	if err != nil {
		return User{}, err
	}
	return user, nil
}

// querier is satisfied by both *pgxpool.Pool and pgx.Tx, so helpers work
// inside and outside a transaction without duplication.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func loadRoles(ctx context.Context, q querier, userID uuid.UUID) ([]Role, error) {
	rows, err := q.Query(ctx, `SELECT role::text FROM role_grants WHERE user_id = $1 ORDER BY role`, userID)

	if err != nil {
		return nil, fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()

	roles := []Role{}
	for rows.Next() {
		var r Role
		if err := rows.Scan(&r); err != nil {
			return nil, fmt.Errorf("scan role: %w", err)
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// insertSession caps the new expiry at the family's absolute age limit, so a
// continuously refreshed session still ends. maxAge of 0 skips the cap, which
// is correct for the first session in a family.
func insertSession(ctx context.Context, tx pgx.Tx, userID, familyID uuid.UUID, hash []byte, userAgent, ip string, ttl, maxAge time.Duration) (Session, error) {
	sess := Session{UserID: userID, FamilyID: familyID}

	const q = `
		INSERT INTO sessions (user_id, family_id, refresh_hash, user_agent, request_ip, expires_at)
		SELECT $1, $2, $3, $4, $5,
			CASE WHEN $7::interval = '0'::interval THEN now() + $6::interval
			ELSE LEAST(
				now() + $6::interval,
				COALESCE((SELECT min(created_at) FROM sessions WHERE family_id = $2), now()) + $7::interval
			) END
		RETURNING id, expires_at`

	err := tx.QueryRow(ctx, q, userID, familyID, hash, userAgent, ip, ttl, maxAge).
		Scan(&sess.ID, &sess.ExpiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return sess, nil
}

func (s *Store) GrantRole(ctx context.Context, userID uuid.UUID, role Role) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO role_grants (user_id, role) VALUES ($1, $2::user_role)
		 ON CONFLICT DO NOTHING`, userID, string(role))
	if err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	return nil
}

// func (s *Store) GetAny(ctx context.Context, id uuid.UUID) (Space, error) {
// 	row, err := s.q.GetSpaceByID(ctx, id)
// 	if errors.Is(err, pgx.ErrNoRows) {
// 		return Space{}, ErrNotFound
// 	}
// 	if err != nil {
// 		return Space{}, fmt.Errorf("get space: %w", err)
// 	}
// 	return toDomain(row)
// }

// RetryAfter reports how long until the oldest challenge in the window expires,
// which is the soonest a blocked caller can succeed.
func (s *Store) RetryAfter(ctx context.Context, phone string, window time.Duration) (time.Duration, error) {
	var seconds float64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(EXTRACT(EPOCH FROM (MIN(created_at) + $2::interval - now())), 0)
		FROM otp_challenges
		WHERE phone_e164 = $1 AND created_at > now() - $2::interval`,
		phone, window).Scan(&seconds)
	if err != nil {
		return 0, fmt.Errorf("compute otp retry-after: %w", err)
	}
	if seconds < 1 {
		return time.Second, nil
	}
	return time.Duration(seconds) * time.Second, nil
}

func (s *Store) SetDisplayName(ctx context.Context, id uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, id, name)
	if err != nil {
		return fmt.Errorf("set display name: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrOTPNotFound
	}
	return nil
}
