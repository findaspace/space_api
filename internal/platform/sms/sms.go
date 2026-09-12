package sms

import (
	"context"
	"log/slog"
)

type Sender interface {
	Send(ctx context.Context, toE164, body string) error
}

type LogSender struct{ Log *slog.Logger }

func (s LogSender) Send(ctx context.Context, toE164, body string) error {
	s.Log.InfoContext(ctx, "sms not sent (log sender)",
		slog.String("to", toE164),
		slog.String("body", body),
	)
	return nil

}
