package jobs

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// The dependencies the workers need, declared as interfaces so this package
// imports no domain package.
type MediaProcessor interface {
	Process(ctx context.Context, spaceID, mediaID uuid.UUID) error
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

type Deps struct {
	Media      MediaProcessor
	Bookings   HoldExpirer
	Payouts    PayoutReconciler
	Uploads    UploadSweeper
	Ledger     LedgerAuditor
	Log        *slog.Logger
	StuckAfter time.Duration
}

type ProcessMediaWorker struct {
	river.WorkerDefaults[ProcessMediaArgs]
	deps Deps
}

func (w *ProcessMediaWorker) Work(ctx context.Context, job *river.Job[ProcessMediaArgs]) error {
	return w.deps.Media.Process(ctx, job.Args.SpaceID, job.Args.MediaID)
}

type ExpireHoldsWorker struct {
	river.WorkerDefaults[ExpireHoldsArgs]
	deps Deps
}

// Hold expiry is already correct without this: Hold() sweeps lapsed rows before
// inserting. This exists so a host's calendar stops showing a dead hold, not
// for correctness.
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
		w.deps.Log.WarnContext(ctx, "resolved payouts a webhook never reported",
			slog.Int("count", n))
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

// A non-zero trial balance means something wrote entries outside Post. It
// should be impossible, which is exactly why it is worth checking hourly.
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

func Register(deps Deps) (*river.Workers, error) {
	workers := river.NewWorkers()

	river.AddWorker(workers, &ProcessMediaWorker{deps: deps})
	river.AddWorker(workers, &ExpireHoldsWorker{deps: deps})
	river.AddWorker(workers, &ReconcilePayoutsWorker{deps: deps})
	river.AddWorker(workers, &SweepUploadsWorker{deps: deps})
	river.AddWorker(workers, &TrialBalanceWorker{deps: deps})

	return workers, nil
}

// Periodic returns the schedule. River elects one client as leader, so with
// three worker replicas these still run once each interval, not three times.
func Periodic() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(5*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return ExpireHoldsArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}),

		river.NewPeriodicJob(river.PeriodicInterval(10*time.Minute),
			func() (river.JobArgs, *river.InsertOpts) { return ReconcilePayoutsArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}),

		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return SweepUploadsArgs{}, nil }, nil),

		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return TrialBalanceArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true}),
	}
}
