// Package jobs runs slow or unreliable work (talking to Shopify) outside web requests.
//
// A feature calls Enqueue inside its db.InTenant transaction, which saves the job in the jobs table
// together with the change. The worker (cmd/worker) claims ready jobs from that table (Postgres) and
// runs their Handler. A failed job is retried later, with growing waits; after several failures it's
// left dead, its last error kept, for a person to look at.
//
// A job can run more than once (a worker can die after finishing a job but before recording it), so
// handlers must be safe to repeat ("make Shopify match the plants now", not "add one to Shopify's
// stock").
package jobs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Job is one piece of work for one nursery.
type Job struct {
	ID             int64           `json:"id"`
	OrganizationID int64           `json:"organizationId"`
	Kind           string          `json:"kind"`    // e.g. "shopify.sync_product"
	Payload        json.RawMessage `json:"payload"` // the kind's details, e.g. {"productId": 12}
	Attempt        int             `json:"-"`       // 1 the first time it runs
}

// Handler does one kind of job. Returning an error retries the job later.
type Handler func(ctx context.Context, job Job) error

// Enqueue adds a job for orgID. Call it with the Queries of the transaction that makes the change,
// so the job is saved if and only if the change is.
func Enqueue(ctx context.Context, q *dbgen.Queries, orgID int64, kind string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("job %s payload: %w", kind, err)
	}
	if err := q.EnqueueJob(ctx, dbgen.EnqueueJobParams{OrganizationID: orgID, Kind: kind, Payload: encoded}); err != nil {
		return fmt.Errorf("enqueue %s: %w", kind, err)
	}
	return nil
}
