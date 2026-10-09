package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Everything the workers need, as interfaces, so this package imports no
// domain package and cannot take part in an import cycle.

type SearchIndexer interface {
	RefreshStats(ctx context.Context) (int64, error)
}

type MediaProcessor interface {
	Process(ctx context.Context, spaceID, mediaID uuid.UUID) error
	ProcessVideo(ctx context.Context, videoID uuid.UUID) error
	SweepVideos(ctx context.Context, olderThan time.Duration) (int, error)
}

type HoldExpirer interface {
	ExpireHolds(ctx context.Context) (int64, error)
}

type PayoutReconciler interface {
	Reconcile(ctx context.Context, olderThan time.Duration, limit int32) (int, error)
}

type UploadSweeper interface {
	SweepAbandoned(ctx context.Context, olderThan time.Duration) (int, error)
}

type LedgerAuditor interface {
	TrialBalance(ctx context.Context) (int64, error)
}

type BookingAdvancer interface {
	Advance(ctx context.Context) (started, completed int64, err error)
}

type ReviewRevealer interface {
	RevealExpired(ctx context.Context) (int64, error)
}

type NotificationDeliverer interface {
	Deliver(ctx context.Context, notificationID uuid.UUID) error
}

type DeletionCleaner interface{ Sweep(context.Context) error }

type Deps struct {
	Cleanup       DeletionCleaner
	Media         MediaProcessor
	Bookings      HoldExpirer
	Payouts       PayoutReconciler
	Uploads       UploadSweeper
	Ledger        LedgerAuditor
	Advancer      BookingAdvancer
	Reviews       ReviewRevealer
	Notifications NotificationDeliverer
	Log           *slog.Logger
	StuckAfter    time.Duration
	Search        SearchIndexer
	Routes        ListingRouter
}

type ProcessMediaWorker struct {
	river.WorkerDefaults[ProcessMediaArgs]
	deps Deps
}

func (w *ProcessMediaWorker) Work(ctx context.Context, job *river.Job[ProcessMediaArgs]) error {
	return w.deps.Media.Process(ctx, job.Args.SpaceID, job.Args.MediaID)
}

type DeliverNotificationWorker struct {
	river.WorkerDefaults[DeliverNotificationArgs]
	deps Deps
}

func (w *DeliverNotificationWorker) Work(ctx context.Context, job *river.Job[DeliverNotificationArgs]) error {
	return w.deps.Notifications.Deliver(ctx, job.Args.NotificationID)
}

type RefreshSearchStatsWorker struct {
	river.WorkerDefaults[RefreshSearchStatsArgs]
	deps Deps
}

func (w *RefreshSearchStatsWorker) Work(ctx context.Context, job *river.Job[RefreshSearchStatsArgs]) error {
	n, err := w.deps.Search.RefreshStats(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "refreshed search stats", slog.Int64("listings", n))
	}
	return nil
}

// NextRetry backs off quadratically: 30s, 2m, 4.5m, 8m. Paired with
// MaxAttempts 5 at insert time, an SMS is abandoned within about 15 minutes
// rather than arriving the next morning.
func (w *DeliverNotificationWorker) NextRetry(job *river.Job[DeliverNotificationArgs]) time.Time {
	return time.Now().Add(time.Duration(job.Attempt*job.Attempt) * 30 * time.Second)
}

type ExpireHoldsWorker struct {
	river.WorkerDefaults[ExpireHoldsArgs]
	deps Deps
}

func (w *ExpireHoldsWorker) Work(ctx context.Context, job *river.Job[ExpireHoldsArgs]) error {
	n, err := w.deps.Bookings.ExpireHolds(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "expired stale holds", slog.Int64("count", n))
	}
	return nil
}

type ReconcilePayoutsWorker struct {
	river.WorkerDefaults[ReconcilePayoutsArgs]
	deps Deps
}

func (w *ReconcilePayoutsWorker) Work(ctx context.Context, job *river.Job[ReconcilePayoutsArgs]) error {
	n, err := w.deps.Payouts.Reconcile(ctx, w.deps.StuckAfter, 50)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.WarnContext(ctx, "resolved payouts a webhook never reported", slog.Int("count", n))
	}
	return nil
}

type SweepUploadsWorker struct {
	river.WorkerDefaults[SweepUploadsArgs]
	deps Deps
}

func (w *SweepUploadsWorker) Work(ctx context.Context, job *river.Job[SweepUploadsArgs]) error {
	n, err := w.deps.Uploads.SweepAbandoned(ctx, 2*time.Hour)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "removed abandoned uploads", slog.Int("count", n))
	}
	return nil
}

type TrialBalanceWorker struct {
	river.WorkerDefaults[TrialBalanceArgs]
	deps Deps
}

func (w *TrialBalanceWorker) Work(ctx context.Context, job *river.Job[TrialBalanceArgs]) error {
	net, err := w.deps.Ledger.TrialBalance(ctx)
	if err != nil {
		return err
	}
	if net != 0 {
		w.deps.Log.ErrorContext(ctx, "LEDGER OUT OF BALANCE", slog.Int64("net_minor", net))
	}
	return nil
}

type AdvanceBookingsWorker struct {
	river.WorkerDefaults[AdvanceBookingsArgs]
	deps Deps
}

type ListingRouter interface {
	Compute(ctx context.Context, spaceID uuid.UUID) error
	Backfill(ctx context.Context, limit int32) (int, error)
}

func (w *AdvanceBookingsWorker) Work(ctx context.Context, job *river.Job[AdvanceBookingsArgs]) error {
	started, completed, err := w.deps.Advancer.Advance(ctx)
	if err != nil {
		return err
	}
	if started+completed > 0 {
		w.deps.Log.InfoContext(ctx, "advanced bookings",
			slog.Int64("leases_started", started), slog.Int64("stays_completed", completed))
	}
	return nil
}

type RevealReviewsWorker struct {
	river.WorkerDefaults[RevealReviewsArgs]
	deps Deps
}

func (w *RevealReviewsWorker) Work(ctx context.Context, job *river.Job[RevealReviewsArgs]) error {
	n, err := w.deps.Reviews.RevealExpired(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "revealed reviews past their window", slog.Int64("count", n))
	}
	return nil
}

func Register(deps Deps) (*river.Workers, error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &SweepDeletedStorageWorker{deps: deps})

	river.AddWorker(workers, &ProcessMediaWorker{deps: deps})
	river.AddWorker(workers, &DeliverNotificationWorker{deps: deps})
	river.AddWorker(workers, &ExpireHoldsWorker{deps: deps})
	river.AddWorker(workers, &ReconcilePayoutsWorker{deps: deps})
	river.AddWorker(workers, &SweepUploadsWorker{deps: deps})
	river.AddWorker(workers, &TrialBalanceWorker{deps: deps})
	river.AddWorker(workers, &AdvanceBookingsWorker{deps: deps})
	river.AddWorker(workers, &RevealReviewsWorker{deps: deps})
	river.AddWorker(workers, &RefreshSearchStatsWorker{deps: deps})
	river.AddWorker(workers, &RouteListingWorker{deps: deps})
	river.AddWorker(workers, &BackfillRoutesWorker{deps: deps})
	river.AddWorker(workers, &ProcessVideoWorker{deps: deps})
	river.AddWorker(workers, &SweepVideosWorker{deps: deps})
	// river.AddWorker(workers, &SweepUploadsWorker{deps: deps})

	return workers, nil
}

// Periodic returns the schedule. River elects one client as leader, so with
// several worker replicas each job still runs once per interval.
func Periodic() []*river.PeriodicJob {
	every := func(d time.Duration, args river.JobArgs, onStart bool) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(d),
			func() (river.JobArgs, *river.InsertOpts) { return args, nil },
			&river.PeriodicJobOpts{RunOnStart: onStart})
	}

	return []*river.PeriodicJob{
		every(5*time.Minute, ExpireHoldsArgs{}, true),
		every(time.Hour, SweepDeletedStorageArgs{}, true),
		every(10*time.Minute, ReconcilePayoutsArgs{}, true),
		every(time.Hour, SweepUploadsArgs{}, false),
		every(time.Hour, TrialBalanceArgs{}, true),
		every(time.Hour, AdvanceBookingsArgs{}, true),
		every(6*time.Hour, RevealReviewsArgs{}, false),
		every(5*time.Minute, RefreshSearchStatsArgs{}, true),
		every(15*time.Minute, BackfillRoutesArgs{}, true),
		every(time.Hour, SweepVideosArgs{}, false),
	}
}

type RouteListingWorker struct {
	river.WorkerDefaults[RouteListingArgs]
	deps Deps
}

func (w *RouteListingWorker) Work(ctx context.Context, job *river.Job[RouteListingArgs]) error {
	return w.deps.Routes.Compute(ctx, job.Args.SpaceID)
}

type BackfillRoutesWorker struct {
	river.WorkerDefaults[BackfillRoutesArgs]
	deps Deps
}

// Catches listings published while OSRM was down. Compute never errors on an
// outage, so without this they would keep their straight-line distance forever.
func (w *BackfillRoutesWorker) Work(ctx context.Context, job *river.Job[BackfillRoutesArgs]) error {
	n, err := w.deps.Routes.Backfill(ctx, 100)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "routed listings", slog.Int("count", n))
	}
	return nil
}

type ProcessVideoWorker struct {
	river.WorkerDefaults[ProcessVideoArgs]
	deps Deps
}

func (w *ProcessVideoWorker) Work(ctx context.Context, job *river.Job[ProcessVideoArgs]) error {
	return w.deps.Media.ProcessVideo(ctx, job.Args.VideoID)
}

// Transcoding holds a worker slot for minutes, so the default twenty-five
// attempts would mean a single bad file occupying a slot for hours. Three is
// enough to survive a restart or a blip in object storage.
func (w *ProcessVideoWorker) Timeout(job *river.Job[ProcessVideoArgs]) time.Duration {
	return 10 * time.Minute
}

type SweepVideosWorker struct {
	river.WorkerDefaults[SweepVideosArgs]
	deps Deps
}

func (w *SweepVideosWorker) Work(ctx context.Context, job *river.Job[SweepVideosArgs]) error {
	n, err := w.deps.Media.SweepVideos(ctx, 90*time.Minute)
	if err != nil {
		return err
	}
	if n > 0 {
		w.deps.Log.InfoContext(ctx, "removed abandoned walkthroughs", slog.Int("count", n))
	}
	return nil
}

type SweepDeletedStorageWorker struct {
	river.WorkerDefaults[SweepDeletedStorageArgs]
	deps Deps
}

func (w *SweepDeletedStorageWorker) Work(ctx context.Context, job *river.Job[SweepDeletedStorageArgs]) error {
	if w.deps.Cleanup == nil {
		return nil
	}
	return w.deps.Cleanup.Sweep(ctx)
}
