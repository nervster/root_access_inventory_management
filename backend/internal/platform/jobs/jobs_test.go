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
	sendErr  error
	waiting  []Job
	done     []int64
	retries  map[int64]time.Duration
	received int
}

func (f *fakeQueue) Send(_ context.Context, jobs []Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.waiting = append(f.waiting, jobs...)
	return nil
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

func (f *fakeQueue) RetryLater(_ context.Context, d Delivery, delay time.Duration) error {
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
	if err := conn.QueryRow(t.Context(), "SELECT count(*) FROM jobs WHERE sent_at IS NULL").Scan(&count); err != nil {
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

func TestRelay(t *testing.T) {
	tx := dbtest.Tx(t)
	org, _ := newOrganizations(t, tx)
	ctx := t.Context()
	err := db.InTenant(ctx, tx, org.ID, func(q *dbgen.Queries) error {
		for i := range 12 {
			if err := Enqueue(ctx, q, org.ID, "test.kind", map[string]int{"n": i}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// SQS is down: nothing is marked sent, so the jobs go next time.
	queue := &fakeQueue{sendErr: errors.New("SQS is down")}
	relay := Relay{Conn: tx, Queue: queue}
	if sent, err := relay.RelayOnce(ctx); err == nil || sent != 0 || countUnsent(t, tx) != 12 {
		t.Fatalf("with SQS down: sent %d, %v, %d unsent; want an error and all 12 unsent", sent, err, countUnsent(t, tx))
	}

	queue.sendErr = nil
	for _, want := range []int{10, 2, 0} {
		if sent, err := relay.RelayOnce(ctx); err != nil || sent != want {
			t.Fatalf("relay: sent %d, %v; want %d", sent, err, want)
		}
	}
	if len(queue.waiting) != 12 || countUnsent(t, tx) != 0 {
		t.Fatalf("queue has %d, %d unsent; want all 12 sent once", len(queue.waiting), countUnsent(t, tx))
	}
	first := queue.waiting[0]
	if first.OrganizationID != org.ID || first.Kind != "test.kind" || string(first.Payload) != `{"n": 0}` {
		t.Errorf("first job = %+v (payload %s), want the first enqueued", first, first.Payload)
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
	_ = queue.Send(t.Context(), []Job{
		{ID: 1, Kind: "ok", Payload: json.RawMessage(`{"name":"first"}`)},
		{ID: 2, Kind: "fails", Payload: json.RawMessage(`{}`)},
		{ID: 3, Kind: "panics", Payload: json.RawMessage(`{}`)},
		{ID: 4, Kind: "unknown", Payload: json.RawMessage(`{}`)},
		{ID: 5, Kind: "ok", Payload: json.RawMessage(`{"name":"fifth"}`)},
	})

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
