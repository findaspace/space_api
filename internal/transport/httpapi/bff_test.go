package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const secret = "0123456789abcdef0123456789abcdef-web"

// seenIP runs a request through TrustBFF and reports the address ClientIP
// resolved, and whether the secret header survived to the handler.
func seenIP(t *testing.T, configured string, header string, xff string) (string, bool) {
	t.Helper()
	var ip string
	var leaked bool

	h := TrustBFF(configured)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip = ClientIP(r, false)
		leaked = r.Header.Get(bffHeader) != ""
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/auth/otp", nil)
	req.RemoteAddr = "34.120.1.1:5000" // the web app's server
	if header != "" {
		req.Header.Set(bffHeader, header)
	}
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return ip, leaked
}

func TestTrustedBFFForwardsClientIP(t *testing.T) {
	ip, _ := seenIP(t, secret, secret, "41.66.12.34")
	if ip != "41.66.12.34" {
		t.Errorf("ip = %s, want the forwarded client address", ip)
	}
}

// The attack this exists to stop: a stranger sets X-Forwarded-For to pick a
// fresh rate-limit bucket for every request.
func TestSpoofedForwardedForIgnoredWithoutSecret(t *testing.T) {
	ip, _ := seenIP(t, secret, "", "1.2.3.4")
	if ip != "34.120.1.1" {
		t.Errorf("ip = %s, want the connecting address; the header was spoofed", ip)
	}
}

func TestWrongSecretIgnored(t *testing.T) {
	ip, _ := seenIP(t, secret, strings.Repeat("x", len(secret)), "1.2.3.4")
	if ip != "34.120.1.1" {
		t.Errorf("ip = %s, want the connecting address", ip)
	}
}

// An unset secret must never mean "trust everyone", including a request that
// sends an empty header hoping to match an empty configuration.
func TestUnconfiguredSecretTrustsNobody(t *testing.T) {
	for _, header := range []string{"", "anything"} {
		ip, _ := seenIP(t, "", header, "1.2.3.4")
		if ip != "34.120.1.1" {
			t.Errorf("header %q: ip = %s, want the connecting address", header, ip)
		}
	}
}

// The secret must not reach handlers or logs, matched or not.
func TestSecretHeaderIsStripped(t *testing.T) {
	for _, header := range []string{secret, "wrong"} {
		if _, leaked := seenIP(t, secret, header, ""); leaked {
			t.Errorf("header %q reached the handler", header)
		}
	}
}

func TestTrustedBFFWithoutForwardedForUsesConnection(t *testing.T) {
	ip, _ := seenIP(t, secret, secret, "")
	if ip != "34.120.1.1" {
		t.Errorf("ip = %s, want the connecting address", ip)
	}
}
