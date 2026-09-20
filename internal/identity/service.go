package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/samnart1/space_api/internal/platform/sms"
)

type ServiceConfig struct {
	OTPTTL         time.Duration
	OTPMaxAttempts int16
	OTPWindow      time.Duration
	OTPMaxPerPhone int
	OTPMaxPerIP    int
	RefreshTTL     time.Duration
	SessionMaxAge  time.Duration
}

type Service struct {
	store  *Store
	hasher *Hasher
	tokens *TokenIssuer
	sender sms.Sender
	log    *slog.Logger
	cfg    ServiceConfig
	now    func() time.Time
}

func NewService(store *Store, hasher *Hasher, tokens *TokenIssuer, sender sms.Sender, log *slog.Logger, cfg ServiceConfig) *Service {
	return &Service{
		store: store, hasher: hasher, tokens: tokens,
		sender: sender, log: log, cfg: cfg, now: time.Now,
	}
}

type OTPResult struct {
	Phone     string
	ExpiresAt time.Time
}

type TokenPair struct {
	User             User
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}

// RequestOTP never reveals whether an account exists. Users are created on
// successful verification, so there is nothing here to enumerate.
func (s *Service) RequestOTP(ctx context.Context, rawPhone, ip string) (OTPResult, error) {
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return OTPResult{}, err
	}

	byPhone, byIP, err := s.store.CountRecent(ctx, phone, ip, s.cfg.OTPWindow)
	if err != nil {
		return OTPResult{}, err
	}
	if byPhone >= s.cfg.OTPMaxPerPhone || byIP >= s.cfg.OTPMaxPerIP {
		return OTPResult{}, ErrTooManyOTP
	}

	code, err := GenerateCode()
	if err != nil {
		return OTPResult{}, err
	}

	ch, err := s.store.CreateChallenge(ctx, phone, ip, s.hasher.Hash(phone, code), s.cfg.OTPTTL, s.cfg.OTPMaxAttempts)
	if err != nil {
		return OTPResult{}, err
	}

	body := fmt.Sprintf("%s is your Space verification code. It expires in %d minutes.",
		code, int(s.cfg.OTPTTL.Minutes()))

	if err := s.sender.Send(ctx, phone, body); err != nil {
		// The challenge row stays. It expires on its own and the user can retry.
		s.log.ErrorContext(ctx, "otp delivery failed",
			slog.String("phone", MaskPhone(phone)), slog.Any("error", err))
		return OTPResult{}, fmt.Errorf("send otp: %w", err)
	}

	return OTPResult{Phone: phone, ExpiresAt: ch.ExpiresAt}, nil
}

func (s *Service) VerifyOTP(ctx context.Context, rawPhone, code, userAgent, ip string) (TokenPair, error) {
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return TokenPair{}, err
	}

	ch, err := s.store.ClaimChallenge(ctx, phone)
	if err != nil {
		return TokenPair{}, err
	}

	// Order matters: the attempt is already recorded by ClaimChallenge, so
	// every path below has been counted against the limit.
	if s.now().After(ch.ExpiresAt) {
		return TokenPair{}, ErrOTPExpired
	}
	if ch.Attempts > ch.MaxAttempts {
		return TokenPair{}, ErrOTPExhausted
	}
	if !s.hasher.Match(phone, code, ch.CodeHash) {
		return TokenPair{}, ErrOTPMismatch
	}

	refresh, digest, err := NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	user, sess, err := s.store.Login(ctx, ch.ID, phone, userAgent, ip, digest, s.cfg.RefreshTTL)
	if err != nil {
		return TokenPair{}, err
	}
	return s.pair(user, sess, refresh)
}

func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent, ip string) (TokenPair, error) {
	if refreshToken == "" {
		return TokenPair{}, ErrSessionInvalid
	}
	oldHash := HashRefresh(refreshToken)

	newToken, newHash, err := NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}

	user, sess, err := s.store.Rotate(ctx, oldHash, newHash, userAgent, ip, s.cfg.RefreshTTL, s.cfg.SessionMaxAge)
	if errors.Is(err, ErrSessionInvalid) {
		// Could be unknown, expired, or replayed. Only the last one is an alarm.
		if diag := s.store.DiagnoseRefresh(ctx, oldHash); errors.Is(diag, ErrSessionReuse) {
			s.log.WarnContext(ctx, "refresh token reuse detected, session family revoked",
				slog.String("ip", ip))
			return TokenPair{}, diag
		}
		return TokenPair{}, ErrSessionInvalid
	}
	if err != nil {
		return TokenPair{}, err
	}
	return s.pair(user, sess, newToken)
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	return s.store.RevokeByRefreshHash(ctx, HashRefresh(refreshToken), "logout")
}

func (s *Service) Me(ctx context.Context, userID uuid.UUID) (User, error) {
	return s.store.UserByID(ctx, userID)
}

func (s *Service) pair(user User, sess Session, refresh string) (TokenPair, error) {
	access, exp, err := s.tokens.Issue(user, sess.ID, s.now())
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		User:             user,
		AccessToken:      access,
		AccessExpiresAt:  exp,
		RefreshToken:     refresh,
		RefreshExpiresAt: sess.ExpiresAt,
	}, nil
}

func (s *Service) GrantHost(ctx context.Context, userID uuid.UUID) error {
	return s.store.GrantRole(ctx, userID, RoleHost)
}
