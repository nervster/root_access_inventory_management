-- The jobs table becomes the queue itself (no SQS): the worker claims ready jobs straight from it,
-- holding each on a lease while it runs, so a worker that dies mid-job doesn't lose it. A failed job
-- waits a while (run_after) and runs again; after 5 failures it's dead, its last error kept.

-- +goose Up
ALTER TABLE jobs RENAME COLUMN sent_at TO started_at; -- first taken by the worker
ALTER TABLE jobs
    ADD COLUMN attempts     integer     NOT NULL DEFAULT 0,
    ADD COLUMN run_after    timestamptz NOT NULL DEFAULT now(), -- not before (waiting to retry)
    ADD COLUMN locked_until timestamptz,                        -- a worker's lease while it runs the job
    ADD COLUMN finished_at  timestamptz,
    ADD COLUMN dead_at      timestamptz,                        -- failed too often: left for a person
    ADD COLUMN last_error   text;
-- Jobs already handed to SQS ran there.
UPDATE jobs SET finished_at = started_at WHERE started_at IS NOT NULL;

DROP INDEX jobs_unsent_idx;
DROP INDEX jobs_sent_at_idx;
-- A job waiting to start isn't added twice (00017); once started, a newer change queues a new one.
DROP INDEX jobs_waiting_once_idx;
CREATE UNIQUE INDEX jobs_waiting_once_idx ON jobs (organization_id, kind, payload) WHERE started_at IS NULL;
CREATE INDEX jobs_ready_idx ON jobs (run_after, id) WHERE finished_at IS NULL AND dead_at IS NULL;
CREATE INDEX jobs_finished_idx ON jobs (finished_at) WHERE finished_at IS NOT NULL;

-- +goose Down
DROP INDEX jobs_finished_idx;
DROP INDEX jobs_ready_idx;
DROP INDEX jobs_waiting_once_idx;
UPDATE jobs SET started_at = NULL WHERE finished_at IS NULL AND dead_at IS NULL; -- back to waiting for SQS
ALTER TABLE jobs DROP COLUMN last_error, DROP COLUMN dead_at, DROP COLUMN finished_at,
    DROP COLUMN locked_until, DROP COLUMN run_after, DROP COLUMN attempts;
ALTER TABLE jobs RENAME COLUMN started_at TO sent_at;
CREATE UNIQUE INDEX jobs_waiting_once_idx ON jobs (organization_id, kind, payload) WHERE sent_at IS NULL;
CREATE INDEX jobs_unsent_idx ON jobs (id) WHERE sent_at IS NULL;
CREATE INDEX jobs_sent_at_idx ON jobs (sent_at) WHERE sent_at IS NOT NULL;
