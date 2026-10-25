package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// fakeQueue keeps jobs in memory and records what the worker told it.
type fakeQueue struct {
	mu       sync.Mutex
	waiting  []Job
	done     []int64
	retries  map[int64]time.Duration
	received int
}

func (f *fakeQueue) add(jobs ...Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waiting = append(f.waiting, jobs...)
}

func (f *fakeQueue) Receive(ctx context.Context) ([]Delivery, error) {
	f.mu.Lock()
	jobs := f.waiting
	f.waiting = nil
	f.mu.Unlock()
	if len(jobs) == 0 {
		pause(ctx, 10*time.Millisecond)
	}
	deliveries := make([]Delivery, len(jobs))
	for i, job := range jobs {
		f.mu.Lock()
		f.received++
		f.mu.Unlock()
		job.Attempt = 1
		deliveries[i] = Delivery{Job: job, Receipt: fmt.Sprint(job.ID)}
	}
	return deliveries, nil
}

func (f *fakeQueue) Done(_ context.Context, d Delivery) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = append(f.done, d.Job.ID)
	return nil
}

func (f *fakeQueue) RetryLater(_ context.Context, d Delivery, delay time.Duration, _ error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.retries == nil {
		f.retries = map[int64]time.Duration{}
	}
	f.retries[d.Job.ID] = delay
	return nil
}

func newOrganizations(t *testing.T, conn db.Conn) (dbgen.Organization, dbgen.Organization) {
	t.Helper()
	var orgs []dbgen.Organization
	for _, slug := range []string{"jobs-test", "jobs-rival"} {
		org, err := dbgen.New(conn).CreateOrganization(t.Context(), dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
		if err != nil {
			t.Fatal(err)
		}
		orgs = append(orgs, org)
	}
	return orgs[0], orgs[1]
}

func countUnsent(t *testing.T, conn db.Conn) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE started_at IS NULL").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestJobIsSavedWithItsChange: a job enqueued in a transaction that fails is never saved.
func TestJobIsSavedWithItsChange(t *testing.T) {
	tx := dbtest.Tx(t)
	org, rival := newOrganizations(t, tx)
	ctx := t.Context()

	failed := errors.New("the change failed")
	err := db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
		if err := Enqueue(ctx, q, org.ID, "test.kind", map[string]int{"productId": 1}); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) || countUnsent(t, tx) != 0 {
		t.Fatalf("after a failed change: %v, %d jobs; want no job", err, countUnsent(t, tx))
	}

	err = db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
		return Enqueue(ctx, q, org.ID, "test.kind", map[string]int{"productId": 1})
	})
	if err != nil || countUnsent(t, tx) != 1 {
		t.Fatalf("after a saved change: %v, %d jobs; want 1", err, countUnsent(t, tx))
	}

	// A nursery can't add jobs for another (row-level security).
	err = db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
		return Enqueue(ctx, q, rival.ID, "test.kind", nil)
	})
	if err == nil {
		t.Error("enqueued a job for another nursery, want an error")
	}
}

func TestPostgresQueue(t *testing.T) {
	tx := dbtest.Tx(t)
	org, _ := newOrganizations(t, tx)
	ctx := t.Context()
	err := db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
		for i := range 7 {
			if err := Enqueue(ctx, q, org.ID, "test.kind", map[string]int{"n": i}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	queue := Postgres{Conn: tx, Batch: 5, Idle: time.Millisecond}
	receive := func() []Delivery {
		t.Helper()
		deliveries, err := queue.Receive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return deliveries
	}

	// Claimed in batches, oldest first; a claimed job isn't handed out again while its lease lasts.
	first := receive()
	if len(first) != 5 || first[0].Job.Kind != "test.kind" || string(first[0].Job.Payload) != `{"n": 0}` || first[0].Job.Attempt != 1 || first[0].Job.OrganizationID != org.ID {
		t.Fatalf("first claim = %+v, want 5 jobs starting with n=0, attempt 1", first)
	}
	if second := receive(); len(second) != 2 {
		t.Fatalf("second claim got %d, want the other 2", len(second))
	}
	if third := receive(); len(third) != 0 {
		t.Fatalf("third claim got %d, want none (all held)", len(third))
	}

	// Finished: never claimed again. Failed: waits, then runs again; after the last attempt, dead.
	if err := queue.Done(ctx, first[0]); err != nil {
		t.Fatal(err)
	}
	failed := first[1]
	if err := queue.RetryLater(ctx, failed, time.Minute, errors.New("Shopify said no")); err != nil {
		t.Fatal(err)
	}
	var lastError string
	var waiting bool
	if err := tx.QueryRow(ctx, "SELECT last_error, run_after > now() FROM jobs WHERE id = $1", failed.Job.ID).Scan(&lastError, &waiting); err != nil || lastError != "Shopify said no" || !waiting {
		t.Errorf("failed job: %q, waiting %v, %v; want the error kept and a wait before it runs again", lastError, waiting, err)
	}
	failed.Job.Attempt = maxAttempts
	if err := queue.RetryLater(ctx, failed, time.Minute, errors.New("still no")); err != nil {
		t.Fatal(err)
	}
	var dead bool
	if err := tx.QueryRow(ctx, "SELECT dead_at IS NOT NULL FROM jobs WHERE id = $1", failed.Job.ID).Scan(&dead); err != nil || !dead {
		t.Errorf("after the last attempt: dead %v, %v; want it left dead", dead, err)
	}

	// A worker that dies mid-job: once its lease runs out, the job is claimed again (attempt 2).
	if _, err := tx.Exec(ctx, "UPDATE jobs SET locked_until = now() - interval '1 second' WHERE id = $1", first[2].Job.ID); err != nil {
		t.Fatal(err)
	}
	again := receive()
	if len(again) != 1 || again[0].Job.ID != first[2].Job.ID || again[0].Job.Attempt != 2 {
		t.Errorf("after the lease ran out: %+v, want job %d again, attempt 2", again, first[2].Job.ID)
	}

	// Finished jobs are cleared after a week; dead ones after a month.
	if _, err := tx.Exec(ctx, "UPDATE jobs SET finished_at = now() - interval '8 days' WHERE id = $1", first[0].Job.ID); err != nil {
		t.Fatal(err)
	}
	if deleted, err := DeleteOldJobs(ctx, tx); err != nil || deleted != 1 {
		t.Errorf("cleared %d, %v; want the week-old finished job only", deleted, err)
	}
}

func TestWaitingJobsAreNotDuplicated(t *testing.T) {
	tx := dbtest.Tx(t)
	org, _ := newOrganizations(t, tx)
	ctx := t.Context()
	enqueue := func() {
		t.Helper()
		err := db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
			return Enqueue(ctx, q, org.ID, "test.sync", map[string]int{"productId": 1})
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	enqueue()
	enqueue() // the same job, still waiting: not added again
	if n := countUnsent(t, tx); n != 1 {
		t.Fatalf("%d waiting, want 1", n)
	}
	// Once a worker has started it, a newer change queues a new one.
	if got, err := (Postgres{Conn: tx}).Receive(ctx); err != nil || len(got) != 1 {
		t.Fatalf("claimed %d, %v", len(got), err)
	}
	enqueue()
	if n := countUnsent(t, tx); n != 1 {
		t.Errorf("%d waiting after starting the first, want the new one", n)
	}
}

func TestWorker(t *testing.T) {
	queue := &fakeQueue{}
	var mu sync.Mutex
	ran := map[int64]string{}
	worker := Worker{Queue: queue, Handlers: map[string]Handler{
		"ok": func(_ context.Context, job Job) error {
			var payload struct{ Name string }
			if err := json.Unmarshal(job.Payload, &payload); err != nil {
				return err
			}
			mu.Lock()
			ran[job.ID] = payload.Name
			mu.Unlock()
			return nil
		},
		"fails":  func(context.Context, Job) error { return errors.New("Shopify said no") },
		"panics": func(context.Context, Job) error { panic("bug") },
	}}
	queue.add([]Job{
		{ID: 1, Kind: "ok", Payload: json.RawMessage(`{"name":"first"}`)},
		{ID: 2, Kind: "fails", Payload: json.RawMessage(`{}`)},
		{ID: 3, Kind: "panics", Payload: json.RawMessage(`{}`)},
		{ID: 4, Kind: "unknown", Payload: json.RawMessage(`{}`)},
		{ID: 5, Kind: "ok", Payload: json.RawMessage(`{"name":"fifth"}`)},
	}...)

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		worker.Run(ctx)
		close(stopped)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		queue.mu.Lock()
		finished := len(queue.done) + len(queue.retries)
		queue.mu.Unlock()
		if finished == 5 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-stopped // Run returns once ctx ends

	if ran[1] != "first" || ran[5] != "fifth" || len(queue.done) != 2 {
		t.Errorf("ran %v, done %v; want jobs 1 and 5 run and removed", ran, queue.done)
	}
	for _, id := range []int64{2, 3, 4} { // failed, panicked, no handler: all retried later
		if queue.retries[id] != 30*time.Second {
			t.Errorf("job %d retry = %v, want in 30s", id, queue.retries[id])
		}
	}
}

func TestRetryDelay(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 8 * time.Minute}
	for i, delay := range want {
		if got := retryDelay(i + 1); got != delay {
			t.Errorf("retryDelay(%d) = %v, want %v", i+1, got, delay)
		}
	}
}
