package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samnart1/space_api/internal/booking"
	"github.com/samnart1/space_api/internal/config"
	"github.com/samnart1/space_api/internal/geo"
	"github.com/samnart1/space_api/internal/jobs"
	"github.com/samnart1/space_api/internal/ledger"
	"github.com/samnart1/space_api/internal/media"
	"github.com/samnart1/space_api/internal/notify"
	"github.com/samnart1/space_api/internal/payout"
	"github.com/samnart1/space_api/internal/platform/objectstore"
	"github.com/samnart1/space_api/internal/platform/osrm"
	"github.com/samnart1/space_api/internal/platform/paystack"
	"github.com/samnart1/space_api/internal/platform/sms"
	"github.com/samnart1/space_api/internal/review"
	"github.com/samnart1/space_api/internal/search"
)

var errNotInWorker = errors.New("not available in the worker process")

// workerOnly stands in for request-scoped dependencies the worker never uses.
// Refusing is better than nil: a misuse reports itself instead of panicking.
type workerOnly struct{}

func (workerOnly) AssertHostOwns(context.Context, uuid.UUID, uuid.UUID) error {
	return errNotInWorker
}

func (workerOnly) DisplayNameFor(context.Context, uuid.UUID) (string, string, error) {
	return "", "", errNotInWorker
}

type workerPhones struct{ pool *pgxpool.Pool }

func (w workerPhones) PhoneFor(ctx context.Context, userID uuid.UUID) (string, error) {
	var phone string
	err := w.pool.QueryRow(ctx, `SELECT phone_e164 FROM users WHERE id = $1`, userID).Scan(&phone)
	return phone, err
}

func buildDeps(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, log *slog.Logger) (jobs.Deps, error) {
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
		return jobs.Deps{}, err
	}

	enqueuer, err := jobs.NewEnqueuer(pool)
	if err != nil {
		return jobs.Deps{}, err
	}

	router := osrm.New(cfg.OSRMBaseURL, cfg.OSRMProfile, cfg.OSRMTimeout)
	geoSvc := geo.NewService(geo.NewStore(pool), router, log, geo.ServiceConfig{
		RadiusM:        cfg.GeoRadiusM,
		MaxLandmarks:   cfg.GeoMaxLandmarks,
		MaxWalkSeconds: 2700,
	})

	mediaSvc := media.NewService(media.NewStore(pool), objects, workerOnly{}, enqueuer, log,
		media.ServiceConfig{MaxBytes: cfg.MediaMaxBytes, MaxPerSpace: cfg.MediaMaxPerSpace})

	payoutSvc := payout.NewService(
		payout.NewStore(pool),
		paystack.New(cfg.PaystackSecret, cfg.PaystackBaseURL, cfg.PaystackTimeout),
		workerOnly{}, log,
		payout.ServiceConfig{HoldDays: cfg.PayoutHoldDays, Currency: "GHS"},
	)

	sender, err := sms.FromConfig(cfg.SMSProvider, cfg.ArkeselAPIKey, cfg.SMSSenderID, cfg.ArkeselSandbox, log)
	if err != nil {
		return jobs.Deps{}, err
	}
	notifySvc := notify.NewService(notify.NewStore(pool), enqueuer, sender, workerPhones{pool: pool}, log)

	searchSvc := search.NewService(search.NewStore(pool), objects)

	payoutSvc.SetNotifier(notifySvc)

	return jobs.Deps{
		Media:         mediaSvc,
		Bookings:      booking.NewStore(pool),
		Payouts:       payoutSvc,
		Uploads:       mediaSvc,
		Ledger:        ledger.NewStore(pool),
		Log:           log,
		StuckAfter:    time.Hour,
		Advancer:      booking.NewStore(pool),
		Reviews:       review.NewService(review.NewStore(pool)),
		Notifications: notifySvc,
		Search:        searchSvc,
		Routes:        geoSvc,
	}, nil
}
