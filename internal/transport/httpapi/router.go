package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/messaging"
	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/payment"
	"github.com/samnart1/space_api/internal/payout"
	"github.com/samnart1/space_api/internal/review"
	"github.com/samnart1/space_api/internal/search"
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
	Calendar     *calendar.Service
	Booking      *booking.Service
	Payment      *payment.Service
	Payout       *payout.Service
	Messaging    *messaging.Service
	Review       *review.Service
	Notify       *notify.Service
	Search       *search.Service
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	auth := authHandler{svc: d.Identity, trustProxy: d.TrustProxy}
	ch := calendarHandler{svc: d.Calendar, listings: d.Listing}
	bh := bookingHandler{svc: d.Booking}
	ph := paymentHandler{svc: d.Payment}
	sh := searchHandler{svc: d.Search}

	authed := RequireAuth(d.Tokens)

	nh := notifyHandler{svc: d.Notify}

	mux.Handle("GET /v1/notifications", authed(http.HandlerFunc(nh.inbox)))
	mux.Handle("POST /v1/notifications/read-all", authed(http.HandlerFunc(nh.markAllRead)))
	mux.Handle("POST /v1/notifications/{id}/read", authed(http.HandlerFunc(nh.markRead)))
	mux.Handle("PUT /v1/notifications/preferences", authed(http.HandlerFunc(nh.setPreference)))

	mux.HandleFunc("GET /v1/search", sh.search)

	mux.Handle("POST /v1/bookings/{id}/pay", authed(http.HandlerFunc(ph.intent)))
	mux.HandleFunc("POST /v1/webhooks/paystack", ph.webhook)

	lh := listingHandler{svc: d.Listing}

	mh := mediaHandler{svc: d.Media, listings: d.Listing}

	mh2 := messagingHandler{svc: d.Messaging}
	rh := reviewHandler{svc: d.Review, listings: d.Listing}

	mux.Handle("POST /v1/spaces/{id}/messages", authed(http.HandlerFunc(mh2.send)))
	mux.Handle("GET /v1/threads", authed(http.HandlerFunc(mh2.inbox)))
	mux.Handle("GET /v1/threads/{id}/messages", authed(http.HandlerFunc(mh2.messages)))
	mux.Handle("POST /v1/threads/{id}/messages", authed(http.HandlerFunc(mh2.reply)))
	mux.Handle("POST /v1/threads/{id}/read", authed(http.HandlerFunc(mh2.markRead)))

	mux.Handle("POST /v1/bookings/{id}/reviews", authed(http.HandlerFunc(rh.write)))
	mux.HandleFunc("GET /v1/listings/{slug}/reviews", rh.forListing)

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

	poh := payoutHandler{svc: d.Payout}
	hostOnly := func(h http.HandlerFunc) http.Handler {
		return Chain(h, authed, RequireRole(identity.RoleHost))
	}

	mux.Handle("PUT /v1/payouts/account", hostOnly(poh.saveAccount))
	mux.Handle("GET /v1/payouts/balance", hostOnly(poh.balance))
	mux.Handle("GET /v1/payouts", hostOnly(poh.list))
	mux.Handle("POST /v1/payouts", hostOnly(poh.request))

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

	mux.HandleFunc("GET /v1/listings/{slug}/quote", ch.quote)
	mux.HandleFunc("GET /v1/listings/{slug}/calendar", ch.publicCalendar)

	mux.Handle("GET /v1/spaces/{id}/rules", authed(http.HandlerFunc(ch.getRules)))
	mux.Handle("PUT /v1/spaces/{id}/rules", authed(http.HandlerFunc(ch.setRules)))
	mux.Handle("GET /v1/spaces/{id}/rate-plans", authed(http.HandlerFunc(ch.listPlans)))
	mux.Handle("POST /v1/spaces/{id}/rate-plans", authed(http.HandlerFunc(ch.createPlan)))
	mux.Handle("DELETE /v1/spaces/{id}/rate-plans/{planId}", authed(http.HandlerFunc(ch.deletePlan)))
	mux.Handle("GET /v1/spaces/{id}/blocks", authed(http.HandlerFunc(ch.listBlocks)))
	mux.Handle("POST /v1/spaces/{id}/blocks", authed(http.HandlerFunc(ch.createBlock)))
	mux.Handle("DELETE /v1/spaces/{id}/blocks/{blockId}", authed(http.HandlerFunc(ch.deleteBlock)))

	mux.Handle("POST /v1/bookings", authed(http.HandlerFunc(bh.create)))
	mux.Handle("GET /v1/bookings", authed(http.HandlerFunc(bh.list)))
	mux.Handle("GET /v1/bookings/{id}", authed(http.HandlerFunc(bh.get)))
	mux.Handle("GET /v1/bookings/{id}/refund-quote", authed(http.HandlerFunc(bh.refundQuote)))
	mux.Handle("POST /v1/bookings/{id}/cancel", authed(http.HandlerFunc(bh.cancel)))
	mux.Handle("POST /v1/bookings/{id}/approve", authed(http.HandlerFunc(bh.approve)))
	mux.Handle("POST /v1/bookings/{id}/decline", authed(http.HandlerFunc(bh.decline)))

	return Chain(mux,
		RequestID,
		SecurityHeaders,
		Logger(d.Logger),
		Recover,
		MaxBytes(d.MaxBodyBytes),
	)
}
