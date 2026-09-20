package paystack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const secret = "sk_test_notreal"

func TestSignatureRoundTrip(t *testing.T) {
	body := []byte(`{"event":"charge.success","data":{"reference":"spc-1","amount":100000}}`)

	if !VerifySignature(secret, body, Sign(secret, body)) {
		t.Fatal("a signature we generated did not verify")
	}
	if len(Sign(secret, body)) != 128 {
		t.Errorf("SHA-512 hex should be 128 chars, got %d", len(Sign(secret, body)))
	}
}

func TestSignatureRejectsTamperedBody(t *testing.T) {
	original := []byte(`{"event":"charge.success","data":{"amount":100000}}`)
	tampered := []byte(`{"event":"charge.success","data":{"amount":1}}`)

	if VerifySignature(secret, tampered, Sign(secret, original)) {
		t.Fatal("tampered body passed verification")
	}
}

func TestSignatureRejectsWrongSecretAndEmpty(t *testing.T) {
	body := []byte(`{"event":"charge.success"}`)

	if VerifySignature("sk_test_other", body, Sign(secret, body)) {
		t.Error("wrong secret passed verification")
	}
	if VerifySignature(secret, body, "") {
		t.Error("empty signature passed verification")
	}
}

// Re-encoding the parsed payload produces different bytes. This is why the
// handler must verify before it parses.
func TestReEncodedBodyDoesNotVerify(t *testing.T) {
	body := []byte(`{"event":"charge.success", "data": {"amount": 100000}}`)
	signature := Sign(secret, body)

	reencoded := []byte(`{"event":"charge.success","data":{"amount":100000}}`)
	if VerifySignature(secret, reencoded, signature) {
		t.Fatal("re-encoded body verified; the test fixture is wrong")
	}
}

func TestInitialize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("auth header = %q", got)
		}
		if r.URL.Path != "/transaction/initialize" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":true,"message":"ok","data":{
			"authorization_url":"https://checkout.paystack.com/abc",
			"access_code":"abc","reference":"spc-1"}}`))
	}))
	defer srv.Close()

	c := New(secret, srv.URL, 5*time.Second)
	out, err := c.Initialize(context.Background(), InitializeRequest{
		Email: "guest@example.com", AmountMinor: 100000, Currency: "GHS", Reference: "spc-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.AccessCode != "abc" || !strings.HasPrefix(out.AuthorizationURL, "https://") {
		t.Errorf("out = %+v", out)
	}
}

// status:false with HTTP 200 is a Paystack failure, not a success.
func TestInitializeTreatsStatusFalseAsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":false,"message":"Invalid email address"}`))
	}))
	defer srv.Close()

	c := New(secret, srv.URL, 5*time.Second)
	_, err := c.Initialize(context.Background(), InitializeRequest{})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "Invalid email") {
		t.Errorf("err = %v", err)
	}
}

func TestParseEvent(t *testing.T) {
	e, err := ParseEvent([]byte(`{"event":"charge.success","data":{
		"id":123,"reference":"spc-1","status":"success",
		"amount":100000,"currency":"GHS","channel":"mobile_money","fees":2000}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.Event != "charge.success" || e.Data.AmountMinor != 100000 || e.Data.Channel != "mobile_money" {
		t.Errorf("event = %+v", e)
	}
}
