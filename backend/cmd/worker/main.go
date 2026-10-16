// Command worker runs Plant Stock's background jobs (internal/platform/jobs), claiming them from the
// jobs table in Postgres. Run it next to the API: make worker
package main

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // time zone database built into the binary; the production image has none

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/config"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
	"github.com/nervster/root_access_inventory_management/backend/internal/shopify"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Stop on Ctrl+C locally, or SIGTERM when Kubernetes stops the container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// The API also migrates at startup; the advisory lock makes running both at once safe.
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	// Photos, to upload product images to Shopify.
	photos, err := storage.New(ctx, cfg.Storage())
	if err != nil {
		return err
	}
	shopifyStore, err := shopify.FromConfig(cfg, pool, photos)
	if err != nil {
		return err
	}

	// Each feature adds its kinds of jobs here.
	handlers := map[string]jobs.Handler{}
	maps.Copy(handlers, shopifyStore.JobHandlers())

	slog.Info("worker started", "jobKinds", len(handlers))
	var loops sync.WaitGroup
	loops.Go(func() { jobs.Worker{Queue: jobs.Postgres{Conn: pool}, Handlers: handlers}.Run(ctx) })
	// Hourly, clear out finished jobs (after a week) and dead ones (after a month).
	loops.Go(func() {
		every(ctx, time.Hour, func() {
			if deleted, err := jobs.DeleteOldJobs(ctx, pool); err != nil && ctx.Err() == nil {
				slog.Error("clearing old jobs failed", "error", err)
			} else if deleted > 0 {
				slog.Info("cleared old jobs", "count", deleted)
			}
		})
	})
	// Each minute, check every connected store for new or changed orders (webhooks need a public
	// address, so this is how orders arrive locally, and a safety net when deployed).
	loops.Go(func() {
		every(ctx, time.Minute, func() {
			if err := shopify.QueueOrderImports(ctx, pool); err != nil && ctx.Err() == nil {
				slog.Error("queueing order imports failed", "error", err)
			}
		})
	})
	// Every 10 minutes, queue the nightly drift check for stores where it's now 3 am.
	loops.Go(func() {
		every(ctx, 10*time.Minute, func() {
			if err := shopify.QueueDriftChecks(ctx, pool); err != nil && ctx.Err() == nil {
				slog.Error("queueing drift checks failed", "error", err)
			}
		})
	})
	loops.Wait() // both return once ctx ends (and running jobs finish)

	slog.Info("worker stopped")
	return nil
}

// every runs fn now and then every interval, until ctx ends.
func every(ctx context.Context, interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		fn()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
