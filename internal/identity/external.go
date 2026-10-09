package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samnart1/space_api/internal/platform/postgres"
)

var ErrIdentityConflict = errors.New("identity: sign-in method belongs to another account")

type ExternalIdentity struct {
	Provider, Issuer, Subject, Email string
	ExpiresAt                        time.Time
}
type ExternalVerifier struct {
	clients map[string]string
	keys    map[string]oidc.KeySet
}

func NewExternalVerifier(clients map[string]string) *ExternalVerifier {
	ctx := oidc.ClientContext(context.Background(), &http.Client{Timeout: 10 * time.Second})
	return &ExternalVerifier{clients: clients, keys: map[string]oidc.KeySet{
		"google":    oidc.NewRemoteKeySet(ctx, "https://www.googleapis.com/oauth2/v3/certs"),
		"microsoft": oidc.NewRemoteKeySet(ctx, "https://login.microsoftonline.com/common/discovery/v2.0/keys"),
		"apple":     oidc.NewRemoteKeySet(ctx, "https://appleid.apple.com/auth/keys"),
	}}
}
func (v *ExternalVerifier) Verify(ctx context.Context, provider, raw, nonce string) (ExternalIdentity, error) {
	client := v.clients[provider]
	if client == "" || len(nonce) < 32 || len(nonce) > 128 || len(raw) > 16384 {
		return ExternalIdentity{}, ErrSessionInvalid
	}
	issuer := ""
	switch provider {
	case "google":
		issuer = "https://accounts.google.com"
	case "apple":
		issuer = "https://appleid.apple.com"
	case "microsoft":
		// Choose only a tenant-scoped Microsoft issuer, never a caller-supplied URL.
		claims := jwt.MapClaims{}
		if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
			return ExternalIdentity{}, ErrSessionInvalid
		}
		tenant, _ := claims["tid"].(string)
		id, err := uuid.Parse(tenant)
		if err != nil || id.String() != strings.ToLower(tenant) {
			return ExternalIdentity{}, ErrSessionInvalid
		}
		issuer = "https://login.microsoftonline.com/" + tenant + "/v2.0"
	default:
		return ExternalIdentity{}, ErrSessionInvalid
	}
	token, err := oidc.NewVerifier(issuer, v.keys[provider], &oidc.Config{ClientID: client, SupportedSigningAlgs: []string{"RS256"}}).Verify(ctx, raw)
	if err != nil || token.Nonce != nonce || token.Subject == "" || len(token.Subject) > 255 {
		return ExternalIdentity{}, ErrSessionInvalid
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := token.Claims(&claims); err != nil {
		return ExternalIdentity{}, ErrSessionInvalid
	}
	return ExternalIdentity{Provider: provider, Issuer: token.Issuer, Subject: token.Subject, Email: claims.Email, ExpiresAt: token.Expiry}, nil
}

// ExternalLogin never identifies or merges a user by email. Linking requires
// an authenticated target account and proof of the provider account.
func (s *Store) ExternalLogin(ctx context.Context, who ExternalIdentity, raw string, target uuid.UUID, refresh []byte, ttl time.Duration, agent, ip string) (User, Session, error) {
	var user User
	var session Session
	digest := sha256.Sum256([]byte(raw))
	err := postgres.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", who.Provider+"|"+who.Issuer+"|"+who.Subject); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "DELETE FROM external_login_exchanges WHERE expires_at < now()"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO external_login_exchanges(token_hash,expires_at) VALUES($1,$2)", digest[:], who.ExpiresAt.Add(time.Minute)); err != nil {
			var p *pgconn.PgError
			if errors.As(err, &p) && p.Code == "23505" {
				return ErrSessionInvalid
			}
			return err
		}
		var userID uuid.UUID
		err := tx.QueryRow(ctx, "SELECT user_id FROM external_identities WHERE provider=$1 AND issuer=$2 AND subject=$3", who.Provider, who.Issuer, who.Subject).Scan(&userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			userID = target
			if userID == uuid.Nil {
				if err := tx.QueryRow(ctx, "INSERT INTO users DEFAULT VALUES RETURNING id").Scan(&userID); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, "INSERT INTO external_identities(provider,issuer,subject,user_id,email) VALUES($1,$2,$3,$4,NULLIF($5,''))", who.Provider, who.Issuer, who.Subject, userID, who.Email); err != nil {
				var p *pgconn.PgError
				if errors.As(err, &p) && p.Code == "23505" {
					return ErrIdentityConflict
				}
				return err
			}
		} else if target != uuid.Nil && target != userID {
			return ErrIdentityConflict
		}
		var errUser error
		user, errUser = scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id=$1 FOR UPDATE", userID))
		if errUser != nil {
			return errUser
		}
		if user.Status != StatusActive {
			return ErrUserSuspended
		}
		if _, err := tx.Exec(ctx, "INSERT INTO role_grants(user_id,role) VALUES($1,'guest') ON CONFLICT DO NOTHING", userID); err != nil {
			return err
		}
		user.Roles, errUser = loadRoles(ctx, tx, userID)
		if errUser != nil {
			return errUser
		}
		session, errUser = insertSession(ctx, tx, userID, uuid.New(), refresh, agent, ip, ttl, 0)
		return errUser
	})
	if err != nil {
		return User{}, Session{}, err
	}
	return user, session, nil
}
func (s *Service) ExternalLogin(ctx context.Context, who ExternalIdentity, raw string, target uuid.UUID, agent, ip string) (TokenPair, error) {
	refresh, digest, err := NewRefreshToken()
	if err != nil {
		return TokenPair{}, err
	}
	user, session, err := s.store.ExternalLogin(ctx, who, raw, target, digest, s.cfg.RefreshTTL, agent, ip)
	if err != nil {
		return TokenPair{}, fmt.Errorf("external sign-in: %w", err)
	}
	return s.pair(user, session, refresh)
}
func (s *Service) SignInMethods(ctx context.Context, userID uuid.UUID) ([]string, error) {
	rows, err := s.store.pool.Query(ctx, "SELECT provider FROM external_identities WHERE user_id=$1 ORDER BY provider", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	methods := []string{}
	for rows.Next() {
		var method string
		if err := rows.Scan(&method); err != nil {
			return nil, err
		}
		methods = append(methods, method)
	}
	return methods, rows.Err()
}

func (s *Service) RecentSession(ctx context.Context, userID, sessionID uuid.UUID) (bool, error) {
	var recent bool
	err := s.store.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sessions current_session
 WHERE current_session.id=$1 AND current_session.user_id=$2
 AND current_session.revoked_at IS NULL AND current_session.expires_at>now()
 AND (SELECT min(created_at) FROM sessions family WHERE family.family_id=current_session.family_id)>now()-interval '15 minutes')`, sessionID, userID).Scan(&recent)
	return recent, err
}
