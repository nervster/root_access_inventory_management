package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Queue gives the worker jobs to run: Postgres (the jobs table), or a fake in tests.
type Queue interface {
	// Receive returns ready jobs, waiting briefly when there are none. A received job is held for
	// this worker until it's Done or retried (or its lease runs out: the worker died).
	Receive(ctx context.Context) ([]Delivery, error)
	// Done marks a job finished.
	Done(ctx context.Context, d Delivery) error
	// RetryLater makes a failed job run again after delay, or, after its last attempt, leaves it
	// dead for a person to look at.
	RetryLater(ctx context.Context, d Delivery, delay time.Duration, cause error) error
}

// Delivery is a received job and what's needed to finish it.
type Delivery struct {
	Job     Job
	Receipt string // the job's ID
}

// maxAttempts is how often a job runs before it's left dead.
const maxAttempts = 5

// lease is how long a claimed job stays with its worker: well over handlerTimeout plus a wait for a
// free slot, so a slow job isn't claimed again meanwhile; a dead worker's jobs run again after it.
const lease = 5 * time.Minute

// Postgres is the queue in the jobs table itself: features Enqueue jobs in their own transactions,
// and the worker claims ready ones.
type Postgres struct {
	Conn  db.Conn
	Batch int           // jobs claimed at once (default 5)
	Idle  time.Duration // how long Receive waits when no job is ready (default 1s)
}

func (p Postgres) Receive(ctx context.Context) ([]Delivery, error) {
	batch, idle := p.Batch, p.Idle
	if batch <= 0 {
		batch = 5
	}
	if idle <= 0 {
		idle = time.Second
	}
	rows, err := dbgen.New(p.Conn).ClaimJobs(ctx, dbgen.ClaimJobsParams{Max: int32(batch), LeaseSeconds: int32(lease.Seconds())})
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	if len(rows) == 0 {
		pause(ctx, idle)
		return nil, nil
	}
	deliveries := make([]Delivery, len(rows))
	for i, row := range rows {
		deliveries[i] = Delivery{
			Job:     Job{ID: row.ID, OrganizationID: row.OrganizationID, Kind: row.Kind, Payload: json.RawMessage(row.Payload), Attempt: int(row.Attempts)},
			Receipt: strconv.FormatInt(row.ID, 10),
		}
	}
	return deliveries, nil
}

func (p Postgres) Done(ctx context.Context, d Delivery) error {
	return dbgen.New(p.Conn).FinishJob(ctx, d.Job.ID)
}

func (p Postgres) RetryLater(ctx context.Context, d Delivery, delay time.Duration, cause error) error {
	message := cause.Error()
	if d.Job.Attempt >= maxAttempts {
		return dbgen.New(p.Conn).KillJob(ctx, dbgen.KillJobParams{ID: d.Job.ID, LastError: &message})
	}
	return dbgen.New(p.Conn).RetryJob(ctx, dbgen.RetryJobParams{ID: d.Job.ID, DelaySeconds: int32(delay.Seconds()), LastError: &message})
}

// DeleteOldJobs clears finished jobs after a week and dead ones after a month (the worker, hourly).
func DeleteOldJobs(ctx context.Context, conn db.Conn) (int64, error) {
	return dbgen.New(conn).DeleteOldJobs(ctx)
}
