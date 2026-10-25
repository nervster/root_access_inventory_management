-- Background jobs (e.g. "push this product to Shopify"), as an outbox: a job is inserted in the same
-- transaction as the change that needs it, so it exists exactly when that change is saved. The worker
-- (cmd/worker) hands new jobs to SQS, then runs them from there, retrying failures (internal/platform/jobs).

-- +goose Up
CREATE TABLE jobs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    kind            text        NOT NULL, -- what to do, e.g. 'shopify.sync_product'
    payload         jsonb       NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz           -- handed to SQS; NULL = still to send
);
CREATE INDEX jobs_unsent_idx ON jobs (id) WHERE sent_at IS NULL;
CREATE INDEX jobs_sent_at_idx ON jobs (sent_at) WHERE sent_at IS NOT NULL; -- for clearing out old ones

-- Nurseries' requests may only add jobs; the worker (owner role) sends and clears them.
GRANT INSERT ON jobs TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON jobs
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE jobs;
