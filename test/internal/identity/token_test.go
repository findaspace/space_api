package identity

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func testIssuer(t *testing.T, ttl time.Duration) *TokenIssuer {
	t.Helper()
	iss, err := NewTokenIssuer(make([]byte, ed25519.SeedSize), "test", ttl)
	if err != nil {
		t.Fatal(err)
	}
	return iss
}

func TestIssueAndVerify(t *testing.T) {
	iss := testIssuer(t, 15*time.Minute)
	user := User{ID: uuid.New(), Roles: []Role{RoleGuest, RoleHost}}
	sid := uuid.New()

	raw, exp, err := iss.Issue(user, sid, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !exp.After(time.Now()) {
		t.Fatal("token expires in the past")
	}

	claims, err := iss.Verify(raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != user.ID.String() {
		t.Errorf("subject = %q, want %q", claims.Subject, user.ID.String())
	}
	if claims.ID != sid.String() {
		t.Errorf("jti = %q, want %q", claims.IE, sid.String())
	}
	if len(claims.Roles) != 2 {
		t.Errorf("roles = %v", claims.Roles)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	iss := testIssuer(t, time.Minute)
	raw, _, err := iss.Issue(User{ID: uuid.New()}, uuid.New(), time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(raw); err == nil {
		t.Fatal("expired token verified")
	}
}

func TestVerifyRejectsAlgorithmSwap(t *testing.T) {
	iss := testIssuer(t, 15*time.Minute)

	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, AccessClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   uuid.New().String(),
			Audience:  jwt.ClaimStrings{tokenAudience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		Roles: []Role{RoleAdmin},
	})

	raw, err := forged.SignedString([]byte(iss.PublicKey()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(raw); err == nil {
		t.Fatal("algorithm swap accepted: WithValidMethods is missing")
	}
}

func TestRefreshTokenDigest(t *testing.T) {
	tok, digest, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 32 {
		t.Fatalf("digest length = %d, want 32", len(digest))
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Errorf("token %q is not URL-safe", tok)
	}

	other, _, err := NewRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if tok == other {
		t.Fatal("two calls returned the same token")
	}
}
