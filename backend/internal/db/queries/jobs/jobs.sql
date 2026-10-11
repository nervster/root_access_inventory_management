-- Inside one nursery (run through db.InTenant) --

-- name: EnqueueJob :exec
INSERT INTO jobs (organization_id, kind, payload)
VALUES (@organization_id, @kind, @payload);

-- The worker (account-level, every nursery) --

-- name: UnsentJobs :many
-- The oldest jobs not yet handed to SQS. SKIP LOCKED lets several workers relay at once without
-- sending the same job twice.
SELECT id, organization_id, kind, payload
FROM jobs
WHERE sent_at IS NULL
ORDER BY id
LIMIT @max
FOR UPDATE SKIP LOCKED;

-- name: MarkJobsSent :exec
UPDATE jobs
SET sent_at = now()
WHERE id = ANY(@ids::bigint[]);

-- name: DeleteOldJobs :execrows
-- Sent jobs are kept a week, to look into what was done.
DELETE FROM jobs
WHERE sent_at < now() - interval '7 days';
