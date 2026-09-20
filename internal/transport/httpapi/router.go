package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
)

type Deps struct {
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	PingTimeout  time.Duration
	MaxBodyBytes int64
	Version      string
	Commit       string
	Identity     *identity.Service
	Tokens       *identity.TokenIssuer
	TrustProxy   bool
	Listing      *listing.Service
	Media        *media.Service
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	auth := authHandler{svc: d.Identity, trustProxy: d.TrustProxy}

	authed := RequireAuth(d.Tokens)

	lh := listingHandler{svc: d.Listing}

	mh := mediaHandler{svc: d.Media, listings: d.Listing}

	mux.HandleFunc("GET /v1/listings/{slug}/media", mh.listPublic)

	mux.Handle("GET /v1/spaces/{id}/media", authed(http.HandlerFunc(mh.listOwn)))
	mux.Handle("POST /v1/spaces/{id}/media", authed(http.HandlerFunc(mh.requestUpload)))
	mux.Handle("PUT /v1/spaces/{id}/media/order", authed(http.HandlerFunc(mh.reorder)))
	mux.Handle("POST /v1/spaces/{id}/media/{mediaId}/confirm", authed(http.HandlerFunc(mh.confirm)))
	mux.Handle("DELETE /v1/spaces/{id}/media/{mediaId}", authed(http.HandlerFunc(mh.remove)))

	mux.HandleFunc("GET /v1/space-types", lh.spaceTypes)
	mux.HandleFunc("GET /v1/listings/{slug}", lh.getPublic)

	mux.Handle("POST /v1/spaces", authed(http.HandlerFunc(lh.create)))
	mux.Handle("GET /v1/spaces", authed(http.HandlerFunc(lh.list)))
	mux.Handle("GET /v1/spaces/{id}", authed(http.HandlerFunc(lh.getOwn)))
	mux.Handle("PATCH /v1/spaces/{id}", authed(http.HandlerFunc(lh.patch)))
	mux.Handle("POST /v1/spaces/{id}/submit", authed(http.HandlerFunc(lh.submit)))
	mux.Handle("POST /v1/spaces/{id}/withdraw", authed(http.HandlerFunc(lh.withdraw)))
	mux.Handle("POST /v1/spaces/{id}/archive", authed(http.HandlerFunc(lh.archive)))

	mux.Handle("POST /v1/spaces/{id}/publish",
		Chain(http.HandlerFunc(lh.publish), authed, RequireRole(identity.RoleAdmin)))

	mux.Handle("GET /healthz", handleLive(d.Version, d.Commit))
	mux.Handle("GET /readyz", handleReady(d.Pool, d.PingTimeout))

	mux.HandleFunc("POST /v1/auth/otp", auth.requestOTP)
	mux.HandleFunc("POST /v1/auth/verify", auth.verifyOTP)
	mux.HandleFunc("POST /v1/auth/refresh", auth.refresh)
	mux.HandleFunc("POST /v1/auth/logout", auth.logout)

	mux.Handle("GET /v1/me", RequireAuth(d.Tokens)(http.HandlerFunc(auth.me)))
	mux.Handle("GET /.well-known/space-jwks.json", publicKeyHandler(d.Tokens))

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
