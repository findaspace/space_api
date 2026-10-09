package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExternalVerifierChecksSignatureIssuerAudienceExpiryNonceAndTenant(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{"kty": "RSA", "kid": "test", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer server.Close()
	keys := oidc.NewRemoteKeySet(context.Background(), server.URL)
	verifier := &ExternalVerifier{clients: map[string]string{"google": "web-client", "apple": "web-client", "microsoft": "web-client"}, keys: map[string]oidc.KeySet{"google": keys, "apple": keys, "microsoft": keys}}
	nonce := "0123456789abcdef0123456789abcdef0123456789abc"
	tenant := "9188040d-6c67-4c5b-b112-36a304b66dad"
	for _, tc := range []struct {
		name, provider, issuer, aud, nonce, tenant string
		expired, badKey, badAlg, valid             bool
	}{
		{name: "google", provider: "google", issuer: "https://accounts.google.com", aud: "web-client", nonce: nonce, valid: true},
		{name: "apple", provider: "apple", issuer: "https://appleid.apple.com", aud: "web-client", nonce: nonce, valid: true},
		{name: "microsoft", provider: "microsoft", issuer: "https://login.microsoftonline.com/" + tenant + "/v2.0", aud: "web-client", nonce: nonce, tenant: tenant, valid: true},
		{name: "wrong issuer", provider: "google", issuer: "https://attacker.example", aud: "web-client", nonce: nonce},
		{name: "wrong audience", provider: "google", issuer: "https://accounts.google.com", aud: "another-client", nonce: nonce},
		{name: "wrong nonce", provider: "google", issuer: "https://accounts.google.com", aud: "web-client", nonce: "wrong"},
		{name: "expired", provider: "google", issuer: "https://accounts.google.com", aud: "web-client", nonce: nonce, expired: true},
		{name: "bad signature", provider: "google", issuer: "https://accounts.google.com", aud: "web-client", nonce: nonce, badKey: true},
		{name: "bad algorithm", provider: "google", issuer: "https://accounts.google.com", aud: "web-client", nonce: nonce, badAlg: true},
		{name: "forged tenant", provider: "microsoft", issuer: "https://login.microsoftonline.com/" + tenant + "/v2.0", aud: "web-client", nonce: nonce, tenant: "https://attacker.example"},
		{name: "unknown provider", provider: "unknown", issuer: "https://accounts.google.com", aud: "web-client", nonce: nonce},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exp := time.Now().Add(time.Hour)
			if tc.expired {
				exp = time.Now().Add(-time.Hour)
			}
			claims := jwt.MapClaims{"iss": tc.issuer, "sub": "provider-subject", "aud": tc.aud, "exp": exp.Unix(), "iat": time.Now().Unix(), "nonce": tc.nonce, "tid": tc.tenant, "email": "same@example.com"}
			signingKey := key
			if tc.badKey {
				signingKey = other
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test"
			var raw string
			var err error
			if tc.badAlg {
				token = jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
				raw, err = token.SignedString([]byte("not-a-provider-key"))
			} else {
				raw, err = token.SignedString(signingKey)
			}
			if err != nil {
				t.Fatal(err)
			}
			who, err := verifier.Verify(context.Background(), tc.provider, raw, nonce)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && who.Subject != "provider-subject" {
				t.Fatal("identity lost")
			}
		})
	}
}
