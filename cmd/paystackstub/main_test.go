package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fakeAPI records webhooks and verifies them exactly as space_api does.
type fakeAPI struct {
	mu       sync.Mutex
	secret   string
	accepted []map[string]any
	rejected int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("x-paystack-signature") != Sign(f.secret, body) {
		f.rejected++
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var ev map[string]any
	_ = json.Unmarshal(body, &ev)
	f.accepted = append(f.accepted, ev)
}

func setup(t *testing.T, apiSecret string) (*httptest.Server, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{secret: apiSecret}
	apiSrv := httptest.NewServer(api)
	t.Cleanup(apiSrv.Close)

	stub := NewStub("sk_test_local", "", apiSrv.URL)
	srv := httptest.NewServer(stub.Handler())
	t.Cleanup(srv.Close)
	stub.PublicURL = srv.URL
	return srv, api
}

func initialize(t *testing.T, base string) string {
	t.Helper()
	res, err := http.Post(base+"/transaction/initialize", "application/json", strings.NewReader(
		`{"email":"g@x","amount":1820000,"currency":"GHS","reference":"spc-abc",
		  "callback_url":"http://localhost:3000/bookings/0199-booking","metadata":{"booking_id":"0199-booking"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var out struct {
		Status bool `json:"status"`
		Data   struct {
			AuthorizationURL string `json:"authorization_url"`
		} `json:"data"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if !out.Status {
		t.Fatal("initialize failed")
	}
	return out.Data.AuthorizationURL
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func TestPayingSendsASignedWebhookThenRedirects(t *testing.T) {
	srv, api := setup(t, "sk_test_local")

	checkout := initialize(t, srv.URL)
	if !strings.HasPrefix(checkout, srv.URL+"/checkout/") {
		t.Fatalf("authorization_url = %s, want this stub's checkout page", checkout)
	}

	page, _ := http.Get(checkout)
	body, _ := io.ReadAll(page.Body)
	if !strings.Contains(string(body), "GHS 18200.00") {
		t.Errorf("checkout page does not show the amount: %s", body)
	}

	res, err := noRedirect.Post(checkout+"/pay", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect back", res.StatusCode)
	}

	// The webhook has already arrived by the time the browser is sent back.
	if len(api.accepted) != 1 {
		t.Fatalf("webhooks accepted = %d, want 1 before the redirect", len(api.accepted))
	}
	data := api.accepted[0]["data"].(map[string]any)
	if api.accepted[0]["event"] != "charge.success" || data["reference"] != "spc-abc" ||
		data["amount"].(float64) != 1820000 || data["currency"] != "GHS" {
		t.Errorf("webhook = %v, want charge.success for spc-abc, 1820000 GHS", api.accepted[0])
	}

	// Paystack appends trxref and reference to the callback URL.
	loc, _ := url.Parse(res.Header.Get("Location"))
	if loc.Path != "/bookings/0199-booking" || loc.Query().Get("reference") != "spc-abc" || loc.Query().Get("trxref") != "spc-abc" {
		t.Errorf("redirect = %s, want the booking page with trxref and reference", loc)
	}
}

// With the wrong secret the API rejects the webhook, and the stub must not
// pretend the payment went through.
func TestRejectedWebhookIsNotReportedAsPaid(t *testing.T) {
	srv, api := setup(t, "a-different-secret")
	checkout := initialize(t, srv.URL)

	res, _ := noRedirect.Post(checkout+"/pay", "", nil)
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 when the API rejects the signature", res.StatusCode)
	}
	if api.rejected != 1 || len(api.accepted) != 0 {
		t.Errorf("accepted %d, rejected %d", len(api.accepted), api.rejected)
	}
}

// Paying twice, as a double tap does, must not send a second webhook.
func TestPayingTwiceSendsOneWebhook(t *testing.T) {
	srv, api := setup(t, "sk_test_local")
	checkout := initialize(t, srv.URL)
	_, _ = noRedirect.Post(checkout+"/pay", "", nil)
	_, _ = noRedirect.Post(checkout+"/pay", "", nil)
	if len(api.accepted) != 1 {
		t.Errorf("webhooks = %d, want 1", len(api.accepted))
	}
}

func TestCancelReturnsWithoutAWebhook(t *testing.T) {
	srv, api := setup(t, "sk_test_local")
	checkout := initialize(t, srv.URL)
	res, _ := noRedirect.Post(checkout+"/cancel", "", nil)
	if res.StatusCode != http.StatusSeeOther || len(api.accepted) != 0 {
		t.Errorf("cancel: status %d, webhooks %d; want a redirect and no webhook", res.StatusCode, len(api.accepted))
	}
}

func TestSignatureIsSHA512Hex(t *testing.T) {
	if len(Sign("k", []byte("body"))) != 128 {
		t.Error("an HMAC-SHA512 hex digest is 128 characters")
	}
}

func TestCallbackKeepsExistingQuery(t *testing.T) {
	got := callbackWith("http://x/bookings/1?from=listing", "r1")
	u, _ := url.Parse(got)
	if u.Query().Get("from") != "listing" || u.Query().Get("reference") != "r1" {
		t.Errorf("callbackWith = %s", got)
	}
}
