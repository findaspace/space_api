package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Deps struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	PingTimeout  time.Duration
	MaxBodyBytes int64
	Version      string
	Commit       string
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /healthz", handleLive(d.Version, d.Commit))
	mux.Handle("GET /readyz", handleReady(d.Pool, d.PingTimeout))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	}))

	return Chain(mux,
		RequestID,
		SecurityHeaders,
		Logger(d.Logger),
		Recover,
		MaxBytes(d.MaxBodyBytes),
	)
}
