// Command paystackstub stands in for Paystack during local development, so a
// booking can be paid for end to end in a real browser without an account.
//
// It does what Paystack does, in the same order: initialize returns a checkout
// URL; the checkout page takes the "payment"; the API receives a signed
// charge.success webhook; and only then is the browser sent back to the
// callback URL. The webhook is sent before the redirect on purpose, and it is
// the webhook, never the redirect, that confirms a booking. The web app has to
// cope with the redirect arriving first in production, and it does, by waiting.
//
// Never deploy this. It accepts any secret and confirms any payment.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type transaction struct {
	Reference   string
	Email       string
	AmountMinor int64
	Currency    string
	CallbackURL string
	Metadata    map[string]any
	Paid        bool
}

type Stub struct {
	Secret     string
	PublicURL  string // where browsers reach this stub
	WebhookURL string // the API's Paystack webhook endpoint
	Client     *http.Client

	mu   sync.Mutex
	txns map[string]*transaction
	seq  atomic.Int64
}

func NewStub(secret, publicURL, webhookURL string) *Stub {
	s := &Stub{Secret: secret, PublicURL: strings.TrimRight(publicURL, "/"), WebhookURL: webhookURL,
		Client: &http.Client{Timeout: 10 * time.Second}, txns: map[string]*transaction{}}
	s.seq.Store(4_000_000_000)
	return s
}

func (s *Stub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /transaction/initialize", s.initialize)
	mux.HandleFunc("GET /checkout/{ref}", s.checkout)
	mux.HandleFunc("POST /checkout/{ref}/pay", s.pay)
	mux.HandleFunc("POST /checkout/{ref}/cancel", s.cancel)
	return mux
}

func (s *Stub) initialize(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email       string         `json:"email"`
		Amount      int64          `json:"amount"`
		Currency    string         `json:"currency"`
		Reference   string         `json:"reference"`
		CallbackURL string         `json:"callback_url"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Reference == "" || in.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"status": false, "message": "invalid initialize request"})
		return
	}

	s.mu.Lock()
	s.txns[in.Reference] = &transaction{Reference: in.Reference, Email: in.Email, AmountMinor: in.Amount,
		Currency: in.Currency, CallbackURL: in.CallbackURL, Metadata: in.Metadata}
	s.mu.Unlock()

	code := make([]byte, 16)
	_, _ = rand.Read(code)
	writeJSON(w, http.StatusOK, map[string]any{
		"status": true, "message": "Authorization URL created",
		"data": map[string]any{
			"authorization_url": s.PublicURL + "/checkout/" + url.PathEscape(in.Reference),
			"access_code":       hex.EncodeToString(code),
			"reference":         in.Reference,
		},
	})
}

var page = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Test checkout</title>
<style>body{font-family:system-ui,sans-serif;max-width:420px;margin:40px auto;padding:0 16px;color:#111827}
.warn{background:#FDECEA;color:#B42318;padding:10px 12px;border-radius:8px;font-size:14px}
.amount{font-size:34px;font-weight:700;margin:24px 0 4px}button{width:100%;height:48px;border-radius:10px;font-size:17px;margin-top:12px}
.pay{background:#7C3AED;color:#fff;border:0}.cancel{background:#fff;border:1px solid #C9CFDA}</style></head>
<body><p class="warn">Local test checkout. No money moves.</p>
<p class="amount">{{.Currency}} {{.Amount}}</p><p>Reference {{.Reference}}</p>
{{if .Paid}}<p>Already paid.</p>{{else}}
<form method="post" action="/checkout/{{.Reference}}/pay"><button class="pay">Pay with mobile money</button></form>
<form method="post" action="/checkout/{{.Reference}}/cancel"><button class="cancel">Cancel</button></form>{{end}}
</body></html>`))

func (s *Stub) find(ref string) *transaction {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.txns[ref]
}

func (s *Stub) checkout(w http.ResponseWriter, r *http.Request) {
	t := s.find(r.PathValue("ref"))
	if t == nil {
		http.Error(w, "unknown transaction", http.StatusNotFound)
		return
	}
	_ = page.Execute(w, map[string]any{
		"Reference": t.Reference, "Currency": t.Currency, "Paid": t.Paid,
		"Amount": fmt.Sprintf("%d.%02d", t.AmountMinor/100, t.AmountMinor%100),
	})
}

// Sign matches Paystack: HMAC-SHA512 of the exact body, hex encoded, keyed with
// the secret key. The API verifies against the same bytes.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Stub) pay(w http.ResponseWriter, r *http.Request) {
	t := s.find(r.PathValue("ref"))
	if t == nil {
		http.Error(w, "unknown transaction", http.StatusNotFound)
		return
	}

	if !t.Paid {
		body, _ := json.Marshal(map[string]any{
			"event": "charge.success",
			"data": map[string]any{
				"id": s.seq.Add(1), "reference": t.Reference, "status": "success",
				"amount": t.AmountMinor, "currency": t.Currency, "channel": "mobile_money",
				"fees": t.AmountMinor * 195 / 10000, "gateway_response": "Approved",
				"paid_at": time.Now().UTC().Format(time.RFC3339), "metadata": t.Metadata,
			},
		})
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, s.WebhookURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-paystack-signature", Sign(s.Secret, body))

		res, err := s.Client.Do(req)
		if err != nil {
			http.Error(w, "the API did not accept the webhook: "+err.Error(), http.StatusBadGateway)
			return
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("the API rejected the webhook with %d", res.StatusCode), http.StatusBadGateway)
			return
		}
		s.mu.Lock()
		t.Paid = true
		s.mu.Unlock()
	}

	http.Redirect(w, r, callbackWith(t.CallbackURL, t.Reference), http.StatusSeeOther)
}

// cancel returns the guest without a webhook, like closing Paystack's checkout.
func (s *Stub) cancel(w http.ResponseWriter, r *http.Request) {
	t := s.find(r.PathValue("ref"))
	if t == nil {
		http.Error(w, "unknown transaction", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, callbackWith(t.CallbackURL, t.Reference), http.StatusSeeOther)
}

// callbackWith adds trxref and reference the way Paystack does, keeping any
// query the callback URL already had.
func callbackWith(callback, ref string) string {
	u, err := url.Parse(callback)
	if err != nil {
		return callback
	}
	q := u.Query()
	q.Set("trxref", ref)
	q.Set("reference", ref)
	u.RawQuery = q.Encode()
	return u.String()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	addr := env("STUB_ADDR", ":9099")
	stub := NewStub(
		env("SPACE_PAYSTACK_SECRET", "sk_test_local"),
		env("STUB_PUBLIC_URL", "http://localhost:9099"),
		env("STUB_WEBHOOK_URL", "http://localhost:8080/v1/webhooks/paystack"),
	)
	slog.Info("paystack stub listening", "addr", addr, "webhook", stub.WebhookURL)
	if err := http.ListenAndServe(addr, stub.Handler()); err != nil {
		slog.Error("stub stopped", "error", err)
		os.Exit(1)
	}
}
