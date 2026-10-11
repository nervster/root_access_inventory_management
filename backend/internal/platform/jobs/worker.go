package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// handlerTimeout is the longest one job may run (less than visibilityTimeout).
const handlerTimeout = time.Minute

// Worker runs jobs from the queue with their kind's Handler.
type Worker struct {
	Queue      Queue
	Handlers   map[string]Handler // by Job.Kind
	Concurrent int                // jobs run at the same time (default 5)
}

// Run works until ctx ends, then lets running jobs finish before returning.
func (w Worker) Run(ctx context.Context) {
	concurrent := w.Concurrent
	if concurrent <= 0 {
		concurrent = 5
	}
	// A buffered channel as a semaphore: a job takes a slot to start and gives it back when done,
	// so at most `concurrent` run at once.
	slots := make(chan struct{}, concurrent)
	var running sync.WaitGroup
	defer running.Wait()

	for ctx.Err() == nil {
		deliveries, err := w.Queue.Receive(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("receiving jobs failed", "error", err)
			if len(deliveries) == 0 {
				pause(ctx, 5*time.Second) // e.g. SQS unreachable: don't spin
			}
		}
		for _, delivery := range deliveries {
			slots <- struct{}{}
			running.Go(func() {
				defer func() { <-slots }()
				w.process(delivery)
			})
		}
	}
}

// process runs one job and tells the queue how it went. It deliberately doesn't use Run's ctx: on
// shutdown, a started job finishes (within handlerTimeout) rather than being cut off halfway.
func (w Worker) process(d Delivery) {
	ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
	defer cancel()
	job := d.Job
	log := slog.With("job", job.ID, "kind", job.Kind, "organization", job.OrganizationID, "attempt", job.Attempt)

	err := w.run(ctx, job)
	if err == nil {
		if err := w.Queue.Done(ctx, d); err != nil {
			log.Error("job finished but couldn't be removed from the queue; it will run again", "error", err)
		}
		return
	}

	delay := retryDelay(job.Attempt)
	if job.Attempt >= maxAttempts {
		log.Error("job failed for the last time; SQS moves it to the dead-letter queue", "error", err)
	} else {
		log.Warn("job failed; retrying later", "error", err, "retryIn", delay)
	}
	if err := w.Queue.RetryLater(ctx, d, delay); err != nil {
		log.Error("couldn't schedule the retry; it will come back after the queue's timeout", "error", err)
	}
}

// run calls the job's handler, turning a panic into an error so one bad job can't stop the worker.
func (w Worker) run(ctx context.Context, job Job) (err error) {
	handler, ok := w.Handlers[job.Kind]
	if !ok {
		return fmt.Errorf("no handler for job kind %q", job.Kind)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("handler panicked: %v", recovered)
		}
	}()
	return handler(ctx, job)
}

// retryDelay waits longer after each failure: 30s, 1m, 2m, 4m, then 8m.
func retryDelay(attempt int) time.Duration {
	delay := 30 * time.Second
	for i := 1; i < attempt && delay < 8*time.Minute; i++ {
		delay *= 2
	}
	return delay
}

func pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
