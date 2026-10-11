// Command worker runs Plant Stock's background jobs (internal/platform/jobs): it hands new jobs from
// the database to SQS, and runs them from there. Run it next to the API: make worker
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

	queue, err := jobs.NewSQS(ctx, jobs.SQSConfig{Queue: cfg.JobsQueue, Region: cfg.AWSRegion, Endpoint: cfg.SQSEndpoint})
	if err != nil {
		return err
	}

	// Photos, to upload product images to Shopify.
	photos, err := storage.New(ctx, storage.Config{Bucket: cfg.S3Bucket, Region: cfg.AWSRegion, Endpoint: cfg.S3Endpoint})
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

	slog.Info("worker started", "queue", cfg.JobsQueue, "jobKinds", len(handlers))
	var loops sync.WaitGroup
	loops.Go(func() { jobs.Relay{Conn: pool, Queue: queue}.Run(ctx, time.Second) })
	loops.Go(func() { jobs.Worker{Queue: queue, Handlers: handlers}.Run(ctx) })
	loops.Wait() // both return once ctx ends (and running jobs finish)

	slog.Info("worker stopped")
	return nil
}
