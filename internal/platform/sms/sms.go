package sms

import (
	"context"
	"fmt"
	"log/slog"
)

// Sender is the seam between the product and whichever vendor is cheapest or
// most reliable this quarter. Nothing above this interface names a vendor.
type Sender interface {
	Send(ctx context.Context, toE164, body string) error
}

// LogSender prints instead of sending, so development never spends credits.
// It prints live OTP codes, so config refuses it outside development.
type LogSender struct{ Log *slog.Logger }

func (s LogSender) Send(ctx context.Context, toE164, body string) error {
	s.Log.InfoContext(ctx, "sms not sent (log sender)",
		slog.String("to", toE164),
		slog.String("body", body),
	)
	return nil
}

// FromConfig builds the configured sender. Both binaries call it, so the
// choice of vendor lives in exactly one place.
func FromConfig(provider, apiKey, senderID string, sandbox bool, log *slog.Logger) (Sender, error) {
	switch provider {
	case "log":
		return LogSender{Log: log}, nil
	case "arkesel":
		return NewArkesel(apiKey, senderID, sandbox), nil
	}
	return nil, fmt.Errorf("unknown SMS provider %q", provider)
}
