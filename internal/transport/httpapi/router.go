package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/contact"
	"github.com/samnart1/space_api/internal/geo"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/messaging"
	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/payment"
	"github.com/samnart1/space_api/internal/payout"
	"github.com/samnart1/space_api/internal/platform/metrics"
	"github.com/samnart1/space_api/internal/platform/ratelimit"
	"github.com/samnart1/space_api/internal/review"
	"github.com/samnart1/space_api/internal/search"
	"github.com/samnart1/space_api/internal/trust"
)

type Deps struct {
	Pool            *pgxpool.Pool
	Logger          *slog.Logger
	PingTimeout     time.Duration
	MaxBodyBytes    int64
	Version         string
	Commit          string
	Identity        *identity.Service
	Tokens          *identity.TokenIssuer
	External        *identity.ExternalVerifier
	TrustProxy      bool
	Listing         *listing.Service
	Media           *media.Service
	Calendar        *calendar.Service
	Booking         *booking.Service
	Payment         *payment.Service
	Payout          *payout.Service
	Messaging       *messaging.Service
	Review          *review.Service
	Notify          *notify.Service
	Search          *search.Service
	Limits          Limiters
	CORSOrigins     []string
	RequestTimeout  time.Duration
	Metrics         *metrics.Metrics
	Geo             *geo.Service
	BFFSecret       string
	Contact         *contact.Service
	PaymentsEnabled bool
	DirectContact   bool
}

type Limiters struct {
	Auth    *ratelimit.Limiter
	Write   *ratelimit.Limiter
	Read    *ratelimit.Limiter
	Webhook *ratelimit.Limiter
}

func NewRouter(d Deps) http.Handler {
	mux := http.NewServeMux()
	auth := authHandler{svc: d.Identity, trustProxy: d.TrustProxy}
	ch := calendarHandler{svc: d.Calendar, listings: d.Listing}
	// bh := bookingHandler{svc: d.Booking}
	ph := paymentHandler{svc: d.Payment}
	sh := searchHandler{svc: d.Search}
	videoH := videoHandler{svc: d.Media, listings: d.Listing}
	bh := bookingHandler{svc: d.Booking, listings: d.Listing, users: d.Identity}
	msgH := messagingHandler{svc: d.Messaging, listings: d.Listing, users: d.Identity}

	authLimit := RateLimit(d.Limits.Auth, IPKey(d.TrustProxy))
	readLimit := RateLimit(d.Limits.Read, ClientKey(d.TrustProxy))
	writeLimit := RateLimit(d.Limits.Write, ClientKey(d.TrustProxy))
	hookLimit := RateLimit(d.Limits.Webhook, IPKey(d.TrustProxy))

	mux.Handle("POST /v1/auth/otp", authLimit(http.HandlerFunc(auth.requestOTP)))
	mux.Handle("POST /v1/auth/verify", authLimit(http.HandlerFunc(auth.verifyOTP)))
	mux.Handle("POST /v1/auth/refresh", authLimit(http.HandlerFunc(auth.refresh)))
	mux.Handle("POST /v1/auth/logout", authLimit(http.HandlerFunc(auth.logout)))

	authed := RequireAuth(d.Tokens, d.Identity)
	trustH := trustHandler{store: trust.NewStore(d.Pool), users: d.Identity}
	mux.Handle("POST /v1/spaces/{id}/reports", authed(writeLimit(http.HandlerFunc(trustH.report))))
	mux.Handle("GET /v1/threads/{id}/safety", authed(readLimit(http.HandlerFunc(trustH.boundary))))
	mux.Handle("POST /v1/threads/{id}/block", authed(writeLimit(http.HandlerFunc(trustH.block))))
	mux.Handle("DELETE /v1/threads/{id}/block", authed(writeLimit(http.HandlerFunc(trustH.block))))
	mux.Handle("DELETE /v1/me", authed(writeLimit(http.HandlerFunc(trustH.deleteAccount))))
	mux.Handle("GET /v1/admin/reports", Chain(http.HandlerFunc(trustH.reports), authed, RequireRole(identity.RoleAdmin, identity.RoleSupport)))
	mux.Handle("POST /v1/admin/reports/{id}/review", Chain(http.HandlerFunc(trustH.review), authed, RequireRole(identity.RoleAdmin, identity.RoleSupport), writeLimit))

	external := externalHandler{verifier: d.External, users: d.Identity}
	mux.Handle("POST /v1/auth/external", authLimit(http.HandlerFunc(external.login)))
	mux.Handle("POST /v1/me/sign-in-methods", authed(authLimit(http.HandlerFunc(external.link))))
	mux.Handle("POST /v1/me/phone", authed(authLimit(http.HandlerFunc(external.phone))))
	mux.Handle("GET /v1/me/sign-in-methods", authed(http.HandlerFunc(external.methods)))

	nh := notifyHandler{svc: d.Notify}
	contactH := contactHandler{svc: d.Contact}

	mux.Handle("GET /v1/notifications", authed(http.HandlerFunc(nh.inbox)))

	mux.Handle("POST /v1/notifications/read-all", authed(http.HandlerFunc(nh.markAllRead)))
	mux.Handle("POST /v1/notifications/{id}/read", authed(http.HandlerFunc(nh.markRead)))
	mux.Handle("PUT /v1/notifications/preferences", authed(http.HandlerFunc(nh.setPreference)))

	if d.DirectContact {
		mux.Handle("GET /v1/spaces/{id}/contact", authed(readLimit(http.HandlerFunc(contactH.reveal))))
	}

	// mux.HandleFunc("GET /v1/search", sh.search)

	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings/{id}/pay", authed(http.HandlerFunc(ph.intent)))
	}
	// mux.HandleFunc("POST /v1/webhooks/paystack", ph.webhook)

	lh := listingHandler{svc: d.Listing}
	geoH := geoHandler{svc: d.Geo, listings: d.Listing}

	mediaH := mediaHandler{svc: d.Media, listings: d.Listing}

	// msgH := messagingHandler{svc: d.Messaging}
	rh := reviewHandler{svc: d.Review, listings: d.Listing}

	mux.Handle("GET /v1/listings/{slug}/walks", readLimit(http.HandlerFunc(geoH.forListing)))

	// mux.Handle("POST /v1/spaces/{id}/messages", authed(http.HandlerFunc(msgH.send)))
	mux.Handle("GET /v1/threads", authed(http.HandlerFunc(msgH.inbox)))
	mux.Handle("GET /v1/threads/{id}/messages", authed(http.HandlerFunc(msgH.messages)))

	mux.Handle("GET /v1/spaces/{id}/completeness", authed(http.HandlerFunc(lh.completeness)))

	// mux.Handle("POST /v1/threads/{id}/messages", authed(http.HandlerFunc(msgH.reply)))
	mux.Handle("POST /v1/threads/{id}/read", authed(http.HandlerFunc(msgH.markRead)))

	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings/{id}/reviews", authed(http.HandlerFunc(rh.write)))
	}
	// mux.HandleFunc("GET /v1/listings/{slug}/reviews", rh.forListing)

	// mux.HandleFunc("GET /v1/listings/{slug}/media", mediaH.listPublic)

	mux.Handle("GET /v1/spaces/{id}/media", authed(http.HandlerFunc(mediaH.listOwn)))
	// mux.Handle("POST /v1/spaces/{id}/media", authed(http.HandlerFunc(mediaH.requestUpload)))
	mux.Handle("PUT /v1/spaces/{id}/media/order", authed(http.HandlerFunc(mediaH.reorder)))
	mux.Handle("POST /v1/spaces/{id}/media/{mediaId}/confirm", authed(http.HandlerFunc(mediaH.confirm)))
	mux.Handle("DELETE /v1/spaces/{id}/media/{mediaId}", authed(http.HandlerFunc(mediaH.remove)))

	// mux.HandleFunc("GET /v1/space-types", lh.spaceTypes)
	// mux.HandleFunc("GET /v1/listings/{slug}", lh.getPublic)

	// mux.Handle("POST /v1/spaces", authed(http.HandlerFunc(lh.create)))
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

	if d.PaymentsEnabled {
		mux.Handle("PUT /v1/payouts/account", hostOnly(poh.saveAccount))
	}
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/payouts/balance", hostOnly(poh.balance))
	}
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/payouts", hostOnly(poh.list))
	}
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/payouts/account", hostOnly(poh.getAccount))
	}

	mux.Handle("GET /healthz", handleLive(d.Version, d.Commit))
	mux.Handle("GET /readyz", handleReady(d.Pool, d.PingTimeout))

	// mux.HandleFunc("POST /v1/auth/otp", auth.requestOTP)
	// mux.HandleFunc("POST /v1/auth/verify", auth.verifyOTP)
	// mux.HandleFunc("POST /v1/auth/refresh", auth.refresh)
	// mux.HandleFunc("POST /v1/auth/logout", auth.logout)

	mux.Handle("GET /v1/me", authed(http.HandlerFunc(auth.me)))
	mux.Handle("PATCH /v1/me", authed(writeLimit(http.HandlerFunc(auth.patchMe))))
	mux.Handle("GET /.well-known/space-jwks.json", publicKeyHandler(d.Tokens))

	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, Problem{Status: http.StatusNotFound, Title: "not found"})
	}))

	// mux.HandleFunc("GET /v1/listings/{slug}/quote", ch.quote)
	// mux.HandleFunc("GET /v1/listings/{slug}/calendar", ch.publicCalendar)

	mux.Handle("GET /v1/spaces/{id}/rules", authed(http.HandlerFunc(ch.getRules)))
	mux.Handle("PUT /v1/spaces/{id}/rules", authed(http.HandlerFunc(ch.setRules)))
	mux.Handle("GET /v1/spaces/{id}/rate-plans", authed(http.HandlerFunc(ch.listPlans)))
	mux.Handle("POST /v1/spaces/{id}/rate-plans", authed(http.HandlerFunc(ch.createPlan)))
	mux.Handle("DELETE /v1/spaces/{id}/rate-plans/{planId}", authed(http.HandlerFunc(ch.deletePlan)))
	mux.Handle("GET /v1/spaces/{id}/blocks", authed(http.HandlerFunc(ch.listBlocks)))
	mux.Handle("POST /v1/spaces/{id}/blocks", authed(http.HandlerFunc(ch.createBlock)))
	mux.Handle("DELETE /v1/spaces/{id}/blocks/{blockId}", authed(http.HandlerFunc(ch.deleteBlock)))

	// mux.Handle("POST /v1/bookings", authed(http.HandlerFunc(bh.create)))
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/bookings", authed(http.HandlerFunc(bh.list)))
	}
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/bookings/{id}", authed(http.HandlerFunc(bh.get)))
	}
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/bookings/{id}/refund-quote", authed(http.HandlerFunc(bh.refundQuote)))
	}
	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings/{id}/cancel", authed(http.HandlerFunc(bh.cancel)))
	}
	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings/{id}/approve", authed(http.HandlerFunc(bh.approve)))
	}
	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings/{id}/decline", authed(http.HandlerFunc(bh.decline)))
	}

	mux.Handle("GET /v1/search", readLimit(http.HandlerFunc(sh.search)))
	mux.Handle("GET /v1/listings/{slug}", readLimit(http.HandlerFunc(lh.getPublic)))
	mux.Handle("GET /v1/listings/{slug}/media", readLimit(http.HandlerFunc(mediaH.listPublic)))
	if d.PaymentsEnabled {
		mux.Handle("GET /v1/listings/{slug}/quote", readLimit(http.HandlerFunc(ch.quote)))
	}
	mux.Handle("GET /v1/listings/{slug}/calendar", readLimit(http.HandlerFunc(ch.publicCalendar)))
	mux.Handle("GET /v1/listings/{slug}/reviews", readLimit(http.HandlerFunc(rh.forListing)))
	mux.Handle("GET /v1/space-types", readLimit(http.HandlerFunc(lh.spaceTypes)))

	if d.PaymentsEnabled {
		mux.Handle("POST /v1/bookings", authed(writeLimit(http.HandlerFunc(bh.create))))
	}
	mux.Handle("POST /v1/spaces", authed(writeLimit(http.HandlerFunc(lh.create))))
	mux.Handle("POST /v1/spaces/{id}/media", authed(writeLimit(http.HandlerFunc(mediaH.requestUpload))))
	mux.Handle("POST /v1/spaces/{id}/messages", authed(writeLimit(http.HandlerFunc(msgH.send))))
	mux.Handle("POST /v1/threads/{id}/messages", authed(writeLimit(http.HandlerFunc(msgH.reply))))
	if d.PaymentsEnabled {
		mux.Handle("POST /v1/payouts", authed(writeLimit(http.HandlerFunc(poh.request))))
	}

	if d.PaymentsEnabled {
		mux.Handle("POST /v1/webhooks/paystack", hookLimit(http.HandlerFunc(ph.webhook)))
	}

	mux.Handle("POST /v1/spaces/{id}/video", authed(writeLimit(http.HandlerFunc(videoH.request))))
	mux.Handle("POST /v1/spaces/{id}/video/confirm", authed(http.HandlerFunc(videoH.confirm)))
	mux.Handle("GET /v1/spaces/{id}/video", authed(http.HandlerFunc(videoH.own)))
	mux.Handle("DELETE /v1/spaces/{id}/video", authed(http.HandlerFunc(videoH.remove)))
	mux.Handle("GET /v1/listings/{slug}/video", readLimit(http.HandlerFunc(videoH.forListing)))

	return Chain(mux,
		RequestID,
		TrustBFF(d.BFFSecret),
		CORS(d.CORSOrigins),
		SecurityHeaders,
		Logger(d.Logger),
		Recover,
		Timeout(d.RequestTimeout),
		MaxBytes(d.MaxBodyBytes),
		Metrics(d.Metrics),
	)
}
