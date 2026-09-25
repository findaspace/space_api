package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func handleLive(version, commit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, r, http.StatusOK, map[string]string{
			"status":  "ok",
			"version": version,
			"commit":  commit,
		})
	}
}

func handleReady(pool *pgxpool.Pool, timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			WriteProblem(w, r, Problem{
				Status: http.StatusServiceUnavailable,
				Title:  "dependency unavailable",
				Detail: "database is not reachable",
			})
			return
		}
		WriteJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
	}
}
