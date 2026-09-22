package sms

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const arkeselURL = "https://sms.arkesel.com/api/v2/sms/send"

// ArkeselSender uses Arkesel's v2 API. The sender ID must be registered with
// Arkesel and the networks first, and is at most 11 characters.
type ArkeselSender struct {
	apiKey   string
	senderID string
	sandbox  bool
	http     *http.Client
}

func NewArkesel(apiKey, senderID string, sandbox bool) *ArkeselSender {
	return &ArkeselSender{
		apiKey: apiKey, senderID: senderID, sandbox: sandbox,
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

type arkeselRequest struct {
	Sender     string   `json:"sender"`
	Message    string   `json:"message"`
	Recipients []string `json:"recipients"`
	Sandbox    bool     `json:"sandbox,omitempty"`
}

// Send satisfies Sender. Only the top-level status is trusted: the shape of the
// data payload varies between Arkesel's own examples, so nothing downstream
// depends on it.
func (s *ArkeselSender) Send(ctx context.Context, toE164, body string) error {
	payload, err := json.Marshal(arkeselRequest{
		Sender: s.senderID, Message: body,
		Recipients: []string{toE164}, Sandbox: s.sandbox,
	})
	if err != nil {
		return fmt.Errorf("encode sms: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, arkeselURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build sms request: %w", err)
	}
	req.Header.Set("api-key", s.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("send sms: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)

	if resp.StatusCode >= 300 || out.Status != "success" {
		return fmt.Errorf("arkesel rejected sms (http %d): %s", resp.StatusCode, out.Message)
	}
	return nil
}
