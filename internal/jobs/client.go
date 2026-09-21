package jobs

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// Enqueuer is what the API holds. It inserts jobs and never works them, so the
// API process runs no worker goroutines at all.
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

func NewEnqueuer(pool *pgxpool.Pool) (*Enqueuer, error) {
	// No Queues and no Workers: an insert-only client needs neither, and it
	// does not need starting or stopping.
	c, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("river client: %w", err)
	}
	return &Enqueuer{client: c}, nil
}

// InsertTx enqueues inside the caller's transaction. This is the whole reason
// for a Postgres-backed queue: the job and the row that justifies it commit
// together, so there is no window where one exists without the other.
func (e *Enqueuer) InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs) error {
	if _, err := e.client.InsertTx(ctx, tx, args, nil); err != nil {
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
