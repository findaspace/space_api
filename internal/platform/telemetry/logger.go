package telemetry

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

func NewLogger(level slog.Level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: level <= slog.LevelDebug,
	}

	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}

func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
