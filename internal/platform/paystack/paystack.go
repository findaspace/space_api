package paystack

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const DefaultBaseURL = "https://api.paystack.co"

type Client struct {
	http   *http.Client
	secret string
	base   string
}

func New(secret, baseURL string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		http:   &http.Client{Timeout: timeout},
		secret: secret,
		base:   baseURL,
	}
}

// NewReference returns a transaction reference. Paystack allows alphanumerics
// plus -, . and =; this stays within the safe subset.
func NewReference() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate reference: %w", err)
	}
	return "spc-" + hex.EncodeToString(b[:]), nil
}

type InitializeRequest struct {
	Email       string         `json:"email"`
	AmountMinor int64          `json:"amount"`
	Currency    string         `json:"currency"`
	Reference   string         `json:"reference"`
	CallbackURL string         `json:"callback_url,omitempty"`
	Channels    []string       `json:"channels,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type InitializeResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	AccessCode       string `json:"access_code"`
	Reference        string `json:"reference"`
}

// Transaction is the subset of Paystack's transaction object we act on. Amounts
// are in the currency's subunit: pesewas for GHS.
type Transaction struct {
	ID              int64          `json:"id"`
	Reference       string         `json:"reference"`
	Status          string         `json:"status"`
	AmountMinor     int64          `json:"amount"`
	Currency        string         `json:"currency"`
	Channel         string         `json:"channel"`
	FeesMinor       int64          `json:"fees"`
	GatewayResponse string         `json:"gateway_response"`
	PaidAt          *time.Time     `json:"paid_at"`
	Metadata        map[string]any `json:"metadata"`
}

type envelope struct {
	Status  bool            `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paystack: %s (http %d)", e.Message, e.StatusCode)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("call paystack: %w", err)
	}
	defer resp.Body.Close()

	// Capped read: a hung or hostile upstream must not be able to exhaust memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return &APIError{StatusCode: resp.StatusCode, Message: "unreadable response"}
	}
	if resp.StatusCode >= 300 || !env.Status {
		return &APIError{StatusCode: resp.StatusCode, Message: env.Message}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("decode data: %w", err)
	}
	return nil
}

func (c *Client) Initialize(ctx context.Context, in InitializeRequest) (InitializeResponse, error) {
	var out InitializeResponse
	if err := c.do(ctx, http.MethodPost, "/transaction/initialize", in, &out); err != nil {
		return InitializeResponse{}, err
	}
	return out, nil
}

// Verify re-reads a transaction from Paystack. Used on the return path, where
// the browser has come back but the webhook may not have arrived yet.
func (c *Client) Verify(ctx context.Context, reference string) (Transaction, error) {
	var out Transaction
	if err := c.do(ctx, http.MethodGet, "/transaction/verify/"+reference, nil, &out); err != nil {
		return Transaction{}, err
	}
	return out, nil
}

// Sign produces the value Paystack puts in x-paystack-signature: HMAC-SHA512
// of the raw body, keyed with the secret, hex encoded.
func Sign(secret string, rawBody []byte) string {
	mac := hmac.New(sha512.New, []byte(secret))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks a webhook. rawBody must be the exact bytes received:
// unmarshalling and re-marshalling changes key order and whitespace, and the
// HMAC will never match.
func VerifySignature(secret string, rawBody []byte, signature string) bool {
	if signature == "" {
		return false
	}
	expected := Sign(secret, rawBody)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) == 1
}

type Event struct {
	Event string      `json:"event"`
	Data  Transaction `json:"data"`
}

func ParseEvent(rawBody []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(rawBody, &e); err != nil {
		return Event{}, fmt.Errorf("parse webhook: %w", err)
	}
	if e.Event == "" {
		return Event{}, fmt.Errorf("webhook has no event field")
	}
	return e, nil
}
