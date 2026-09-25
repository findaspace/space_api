package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/calendar"
	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/geo"
	"github.com/samnart1/space_api/internal/identity"
	"github.com/samnart1/space_api/internal/jobs"
	"github.com/samnart1/space_api/internal/listing"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/messaging"
	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/payment"
	"github.com/samnart1/space_api/internal/payout"
	"github.com/samnart1/space_api/internal/platform/metrics"
	"github.com/samnart1/space_api/internal/platform/objectstore"
	"github.com/samnart1/space_api/internal/platform/osrm"
	"github.com/samnart1/space_api/internal/platform/paystack"
	"github.com/samnart1/space_api/internal/platform/postgres"
	"github.com/samnart1/space_api/internal/platform/ratelimit"
	"github.com/samnart1/space_api/internal/platform/sms"
	"github.com/samnart1/space_api/internal/platform/telemetry"
	"github.com/samnart1/space_api/internal/platform/transcode"
	"github.com/samnart1/space_api/internal/review"
	"github.com/samnart1/space_api/internal/search"
	"github.com/samnart1/space_api/internal/transport/httpapi"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := telemetry.NewLogger(cfg.LogLevel, cfg.LogFormat).With(
		slog.String("service", "space_api"),
		slog.String("version", version),
		slog.String("commit", commit),
	)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.NewPool(ctx, postgres.Config{
		URL:          cfg.DatabaseUrl,
		MaxConns:     cfg.DBMaxConns,
		MinConns:     cfg.DBMinConns,
		ConnLifetime: cfg.DBConnLifetime,
		PingTimeout:  cfg.DBPingTimeout,
	})
	if err != nil {
		return err
	}
	defer pool.Close()

	hasher, err := identity.NewHasher(cfg.OTPPepper)
	if err != nil {
		return err
	}

	tokens, err := identity.NewTokenIssuer(cfg.JWTSeed, cfg.JWTKeyID, cfg.AccessTTL)
	if err != nil {
		return err
	}

	var sender sms.Sender
	switch cfg.SMSProvider {
	case "log":
		sender = sms.LogSender{Log: log}
	default:
		return fmt.Errorf("unknown SPACE_SMS_PROVIDER %q", cfg.SMSProvider)
	}

	identitySvc := identity.NewService(
		identity.NewStore(pool), hasher, tokens, sender, log,
		identity.ServiceConfig{
			OTPTTL:         cfg.OTPTTL,
			OTPMaxAttempts: int16(cfg.OTPMaxAttempts),
			OTPWindow:      cfg.OTPWindow,
			OTPMaxPerPhone: cfg.OTPMaxPerPhone,
			OTPMaxPerIP:    cfg.OTPMaxPerIp,
			RefreshTTL:     cfg.RefreshTTL,
			SessionMaxAge:  cfg.SessionMaxAge,
		})
	// listingSvc := listing.NewService(listing.NewStore(pool), identitySvc)

	objects, err := objectstore.New(ctx, objectstore.Config{
		Endpoint:       cfg.StorageEndpoint,
		Region:         cfg.StorageRegion,
		Bucket:         cfg.StorageBucket,
		AccessKey:      cfg.StorageAccessKey,
		SecretKey:      cfg.StorageSecretKey,
		ForcePathStyle: cfg.StoragePathStyle,
		PublicBaseURL:  cfg.StoragePublicBaseURL,
		PresignTTL:     cfg.UploadURLTTL,
	})
	if err != nil {
		return err
	}
	if !cfg.IsProduction() {
		if err := objects.EnsureBucket(ctx); err != nil {
			return err
		}
	}

	enqueuer, err := jobs.NewEnqueuer(pool)
	if err != nil {
		return err
	}

	osrmClient := osrm.New(cfg.OSRMBaseURL, cfg.OSRMProfile, cfg.OSRMTimeout)

	geoSvc := geo.NewService(geo.NewStore(pool), osrmClient, log, geo.ServiceConfig{
		// RadiusM:        cfg.GeoRadiusM,
		RadiusM:        3000,
		MaxLandmarks:   cfg.GeoMaxLandmarks,
		MaxWalkSeconds: 2700,
	})

	mediaStore := media.NewStore(pool)
	listingSvc := listing.NewService(listing.NewStore(pool), identitySvc, mediaStore, routeQueue{queue: enqueuer})

	transcoder := transcode.New(transcode.Config{
		FFmpegPath:  cfg.FFmpegPath,
		FFprobePath: cfg.FFprobePath,
		Timeout:     cfg.TranscodeTimeout,
		MaxSeconds:  cfg.VideoMaxSeconds,
		MaxHeight:   480,
		CRF:         28,
		MaxRateK:    900,
	})

	mediaSvc := media.NewService(media.NewStore(pool), objects, listingSvc, enqueuer,
		transcoder, log, media.ServiceConfig{
			MaxBytes:      cfg.MediaMaxBytes,
			MaxPerSpace:   cfg.MediaMaxPerSpace,
			UploadTTL:     cfg.UploadURLTTL,
			VideoMaxBytes: cfg.VideoMaxBytes,
			WorkDir:       cfg.TranscodeWorkDir,
		})

	calendarSvc := calendar.NewService(calendar.NewStore(pool), calendar.ServiceConfig{
		Fees: calendar.FeeConfig{
			NightlyBps:    cfg.FeeNightlyBps,
			TermBps:       cfg.FeeTermBps,
			TermCapMonths: cfg.FeeTermCapMonths,
			MinMinor:      cfg.FeeMinMinor,
		},
	})
	bookingSvc := booking.NewService(
		booking.NewStore(pool),
		spaceSource{listings: listingSvc},
		pricer{listings: listingSvc, calendars: calendarSvc},
		booking.ServiceConfig{HoldTTL: cfg.BookingHoldTTL},
	)

	paystackClient := paystack.New(cfg.PaystackSecret, cfg.PaystackBaseURL, cfg.PaystackTimeout)

	paymentSvc := payment.NewService(
		payment.NewStore(pool), paystackClient,
		bookingGateway{bookings: bookingSvc},
		payerSource{identity: identitySvc},
		log,
		payment.ServiceConfig{
			Secret:      cfg.PaystackSecret,
			CallbackURL: cfg.PaystackCallbackURL,
			Channels:    []string{"mobile_money", "card"},
		})

	payoutSvc := payout.NewService(
		payout.NewStore(pool), paystackClient,
		hostSource{identity: identitySvc}, log,
		payout.ServiceConfig{HoldDays: cfg.PayoutHoldDays, Currency: "GHS"},
	)
	paymentSvc.SetTransferHandler(payoutSvc)

	messagingSvc := messaging.NewService(messaging.NewStore(pool), listingSvc)
	reviewSvc := review.NewService(review.NewStore(pool))

	notifySvc := notify.NewService(notify.NewStore(pool), enqueuer, sender,
		phoneSource{identity: identitySvc}, log)

	paymentSvc.SetNotifier(notifySvc)
	payoutSvc.SetNotifier(notifySvc)

	searchSvc := search.NewService(search.NewStore(pool), objects)

	tier := func(perMin int) ratelimit.Tier {
		// Burst is a quarter of the per-minute rate: enough for a page that
		// fires several requests at once, not enough to be a useful weapon.
		burst := perMin / 4
		if burst < 5 {
			burst = 5
		}
		return ratelimit.Tier{PerMin: perMin, Burst: burst}
	}

	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		resp, err := http.Get("http://127.0.0.1:8080/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// if err := transcoder.Available(); err != nil {
	// jobs.Deps{},
	// return fmt.Errorf("worker needs ffmpeg: %w", err)
	// }

	// A disabled limiter is a very high one rather than a nil check at every
	// call site: one code path, and the switch cannot be half-applied.
	scale := 1
	if !cfg.RateLimitEnabled {
		scale = 10_000
	}

	limits := httpapi.Limiters{
		Auth:    ratelimit.New(tier(cfg.RateLimitAuth * scale)),
		Read:    ratelimit.New(tier(cfg.RateLimitRead * scale)),
		Write:   ratelimit.New(tier(cfg.RateLimitWrite * scale)),
		Webhook: ratelimit.New(tier(cfg.RateLimitWebhook * scale)),
	}
	defer func() {
		limits.Auth.Close()
		limits.Read.Close()
		limits.Write.Close()
		limits.Webhook.Close()
	}()

	met := metrics.New("api")
	met.WatchPool(pool)
	go func() {
		if err := met.Serve(ctx, cfg.MetricsAddr, log); err != nil {
			log.Error("metrics server stopped", slog.Any("error", err))
		}
	}()

	router := httpapi.NewRouter(httpapi.Deps{
		Pool:           pool,
		Logger:         log,
		PingTimeout:    cfg.DBPingTimeout,
		MaxBodyBytes:   1 << 20,
		Version:        version,
		Commit:         commit,
		Identity:       identitySvc,
		Tokens:         tokens,
		TrustProxy:     cfg.TrustProxy,
		Listing:        listingSvc,
		Media:          mediaSvc,
		Calendar:       calendarSvc,
		Booking:        bookingSvc,
		Payment:        paymentSvc,
		Payout:         payoutSvc,
		Messaging:      messagingSvc,
		Review:         reviewSvc,
		Notify:         notifySvc,
		Search:         searchSvc,
		Limits:         limits,
		CORSOrigins:    cfg.CORSOrigins,
		RequestTimeout: cfg.RequestTimeout,
		Metrics:        met,
		Geo:            geoSvc,
	})

	log.Info("paystack configured",
		slog.String("base_url", cfg.PaystackBaseURL),
		slog.Bool("test_key", strings.HasPrefix(cfg.PaystackSecret, "sk_test_")))

	return httpapi.Run(ctx, httpapi.ServerConfig{
		Addr:            cfg.HTTPAddr,
		ReadTimeout:     cfg.ReadTimeout,
		WriteTimeout:    cfg.WriteTimeout,
		IdleTimeout:     cfg.IdleTimeout,
		ShutdownTimeout: cfg.ShudownTimeout,
	}, router, log)
}
