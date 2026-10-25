-- name: CreateOrganization :one
INSERT INTO organizations (name, slug, time_zone)
VALUES (@name, @slug, @time_zone)
RETURNING *;

-- name: GetOrganization :one
SELECT * FROM organizations
WHERE id = @id;

-- name: UpdateOrganization :one
-- Changes only the fields that are given (NULL keeps the current value).
UPDATE organizations
SET name      = COALESCE(sqlc.narg(name), name),
    time_zone = COALESCE(sqlc.narg(time_zone), time_zone)
WHERE id = @id
RETURNING *;

-- name: LockOrganization :exec
-- Holds the organization's row until the transaction ends, so changes to its members run one at a time.
SELECT 1 FROM organizations
WHERE id = @id
FOR UPDATE;
