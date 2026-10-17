-- Inside one nursery (run through db.InTenant) --

-- name: EnqueueJob :exec
INSERT INTO jobs (organization_id, kind, payload)
VALUES (@organization_id, @kind, @payload)
ON CONFLICT DO NOTHING; -- the same job already waiting to start (see migration 00028)

-- The worker (account-level, every nursery) --

-- name: ClaimJobs :many
-- Takes up to @max ready jobs (oldest first) for @lease_seconds: due to run, not finished or dead, and
-- not held by a worker (or held past its lease: that worker died). SKIP LOCKED lets several workers
-- claim at once without taking the same job. The choice is made once (MATERIALIZED): inside
-- UPDATE … WHERE id IN (…), Postgres may re-run it, and LIMIT would no longer limit.
WITH chosen AS MATERIALIZED (
    SELECT id FROM jobs
    WHERE finished_at IS NULL AND dead_at IS NULL AND run_after <= now()
      AND (locked_until IS NULL OR locked_until < now())
    ORDER BY run_after, id
    LIMIT @max
    FOR UPDATE SKIP LOCKED
)
UPDATE jobs j
SET started_at = coalesce(j.started_at, now()), attempts = j.attempts + 1,
    locked_until = now() + make_interval(secs => @lease_seconds::integer)
FROM chosen
WHERE j.id = chosen.id
RETURNING j.id, j.organization_id, j.kind, j.payload, j.attempts;

-- name: FinishJob :exec
UPDATE jobs SET finished_at = now(), locked_until = NULL
WHERE id = @id;

-- name: RetryJob :exec
-- Failed: run it again after @delay_seconds.
UPDATE jobs
SET run_after = now() + make_interval(secs => @delay_seconds::integer), locked_until = NULL, last_error = @last_error
WHERE id = @id;

-- name: KillJob :exec
-- Failed for the last time: left for a person to look at.
UPDATE jobs SET dead_at = now(), locked_until = NULL, last_error = @last_error
WHERE id = @id;

-- name: DeleteOldJobs :execrows
-- Finished jobs are kept a week, dead ones a month, to look into what was done.
DELETE FROM jobs
WHERE finished_at < now() - interval '7 days' OR dead_at < now() - interval '30 days';
