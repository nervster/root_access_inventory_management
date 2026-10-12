package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestWorkerOnPostgres runs the real worker on the real queue, several jobs at a time: each job runs
// (once it succeeds) exactly once, and a failing one is retried. It commits (the worker uses a pool,
// not one transaction), so it cleans up its nursery after.
func TestWorkerOnPostgres(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := t.Context()
	slug := fmt.Sprintf("jobs-e2e-%d", time.Now().UnixNano())
	org, err := dbgen.New(pool).CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id = $1", org.ID) })

	const total = 20
	err = db.InTenant(ctx, pool, org.ID, func(q *dbgen.Queries) error {
		for i := range total {
			if err := Enqueue(ctx, q, org.ID, "e2e.count", map[string]int{"n": i}); err != nil {
				return err
			}
		}
		return Enqueue(ctx, q, org.ID, "e2e.flaky", struct{}{})
	})
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	ran := map[string]int{}
	flakyTries := 0
	worker := Worker{
		Queue:      Postgres{Conn: pool, Idle: 10 * time.Millisecond},
		Concurrent: 4,
		Handlers: map[string]Handler{
			"e2e.count": func(_ context.Context, job Job) error {
				mu.Lock()
				ran[string(job.Payload)]++
				mu.Unlock()
				return nil
			},
			"e2e.flaky": func(context.Context, Job) error {
				mu.Lock()
				defer mu.Unlock()
				flakyTries++
				return errors.New("not yet")
			},
		},
	}
	runCtx, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		worker.Run(runCtx)
		close(stopped)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		done := len(ran) == total && flakyTries >= 1
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	<-stopped

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != total {
		t.Fatalf("ran %d different jobs, want %d", len(ran), total)
	}
	for payload, n := range ran {
		if n != 1 {
			t.Errorf("job %s ran %d times, want once", payload, n)
		}
	}
	var finished, retrying int
	err = pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE finished_at IS NOT NULL),
	                                 count(*) FILTER (WHERE kind = 'e2e.flaky' AND finished_at IS NULL AND run_after > now() AND last_error = 'not yet')
	                          FROM jobs WHERE organization_id = $1`, org.ID).Scan(&finished, &retrying)
	if err != nil || finished != total || retrying != 1 {
		t.Errorf("finished %d, flaky waiting to retry %d (%v); want %d and 1", finished, retrying, err, total)
	}
}
