package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	tokenIssuer   = "space_api"
	tokenAudience = "space_clients"
)

type AccessClaims struct {
	jwt.RegisteredClaims
	Roles []Role `json:"roles"`
}

type TokenIssuer struct {
	priv      ed25519.PrivateKey
	pub       ed25519.PublicKey
	keyID     string
	accessTTL time.Duration
}

func NewTokenIssuer(seed []byte, keyID string, accessTTL time.Duration) (*TokenIssuer, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: ed25519 seed must be exactly %d bytes", ed25519.SeedSize)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	return &TokenIssuer{
		priv:      priv,
		pub:       priv.Public().(ed25519.PublicKey),
		keyID:     keyID,
		accessTTL: accessTTL,
	}, nil
}

func (t *TokenIssuer) PublicKey() ed25519.PublicKey { return t.pub }
func (t *TokenIssuer) KeyId() string                { return t.keyID }
func (t *TokenIssuer) AccessTTL() time.Duration     { return t.accessTTL }

func (t *TokenIssuer) Issue(u User, sessionID uuid.UUID, now time.Time) (string, time.Time, error) {
	exp := now.Add(t.accessTTL)

	claims := AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   u.ID.String(),
			Audience:  jwt.ClaimStrings{tokenAudience},
			ID:        sessionID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
		Roles: u.Roles,
	}

	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = t.keyID

	signed, err := tok.SignedString(t.priv)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign access token: %w", err)
	}
	return signed, exp, nil
}

func (t *TokenIssuer) Verify(raw string) (*AccessClaims, error) {
	claims := new(AccessClaims)

	_, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return t.pub, nil },
		jwt.WithValidMethods([]string{"EdDSA"}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrSessionInvalid, err)
	}
	return claims, nil
}

func NewRefreshToken() (token string, digest []byte, err error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("generate refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b[:])
	return token, HashRefresh(token), nil
}

func HashRefresh(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
