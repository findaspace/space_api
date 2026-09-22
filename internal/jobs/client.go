package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Enqueuer inserts jobs and never works them, so the API process runs no
// worker goroutines at all.
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewEnqueuer(pool *pgxpool.Pool) (*Enqueuer, error) {
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Enqueuer{client: c}, nil
}

// InsertTx enqueues inside the caller's transaction, so the job and the row
// that justifies it commit together or not at all.
func (e *Enqueuer) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	return e.InsertTxOpts(ctx, tx, args, nil)
}

func (e *Enqueuer) InsertTxOpts(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) error {
	if _, err := e.client.InsertTx(ctx, tx, args, opts); err != nil {
		return fmt.Errorf("enqueue %s: %w", args.Kind(), err)
	}
	return nil
}

// Insert enqueues outside a transaction. Prefer InsertTx wherever the job
// follows from a database write.
func (e *Enqueuer) Insert(ctx context.Context, args river.JobArgs) error {
	if _, err := e.client.Insert(ctx, args, nil); err != nil {
		return fmt.Errorf("enqueue %s: %w", args.Kind(), err)
	}
	return nil
}
