package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// relayBatch is how many jobs go to SQS at once (SQS's most per call).
const relayBatch = 10

// Relay hands new jobs from the jobs table to the queue.
type Relay struct {
	Conn  db.Conn
	Queue Queue
}

// Run relays until ctx ends: right away while there's a backlog, otherwise checking every interval.
// It also clears out jobs sent over a week ago, once an hour.
func (r Relay) Run(ctx context.Context, interval time.Duration) {
	lastCleanup := time.Time{}
	for {
		sent, err := r.RelayOnce(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("relaying jobs failed; trying again shortly", "error", err)
		}
		if time.Since(lastCleanup) > time.Hour {
			if deleted, err := dbgen.New(r.Conn).DeleteOldJobs(ctx); err != nil && ctx.Err() == nil {
				slog.Error("clearing old jobs failed", "error", err)
			} else if deleted > 0 {
				slog.Info("cleared old jobs", "count", deleted)
			}
			lastCleanup = time.Now()
		}
		if sent == relayBatch && err == nil {
			continue // probably more waiting
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// RelayOnce sends up to relayBatch unsent jobs and marks them sent, returning how many. If sending
// fails, nothing is marked and they go next time. If marking fails after sending, they're sent
// again later: SQS delivers at least once anyway, so handlers already cope with repeats.
func (r Relay) RelayOnce(ctx context.Context) (sent int, err error) {
	err = pgx.BeginFunc(ctx, r.Conn, func(tx pgx.Tx) error {
		q := dbgen.New(tx)
		rows, err := q.UnsentJobs(ctx, relayBatch) // locked until this transaction ends
		if err != nil || len(rows) == 0 {
			return err
		}
		jobs := make([]Job, len(rows))
		ids := make([]int64, len(rows))
		for i, row := range rows {
			jobs[i] = Job{ID: row.ID, OrganizationID: row.OrganizationID, Kind: row.Kind, Payload: json.RawMessage(row.Payload)}
			ids[i] = row.ID
		}
		if err := r.Queue.Send(ctx, jobs); err != nil {
			return err
		}
		sent = len(jobs)
		return q.MarkJobsSent(ctx, ids)
	})
	if err != nil {
		return 0, fmt.Errorf("relay jobs: %w", err)
	}
	return sent, nil
}
