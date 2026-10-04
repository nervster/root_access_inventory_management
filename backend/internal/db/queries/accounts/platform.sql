-- Platform admin, across nurseries (account data only: organizations, users, memberships) --

-- name: ListOrganizationsWithMemberCounts :many
SELECT o.id, o.name, o.slug, o.time_zone, o.status, o.created_at,
       (SELECT count(*) FROM memberships m WHERE m.organization_id = o.id) AS member_count
FROM organizations o
ORDER BY o.name;

-- name: GetOrganizationWithMemberCount :one
SELECT o.id, o.name, o.slug, o.time_zone, o.status, o.created_at,
       (SELECT count(*) FROM memberships m WHERE m.organization_id = o.id) AS member_count
FROM organizations o
WHERE o.id = @id;

-- name: UpdateOrganizationAccount :exec
-- Name and status (suspend or reactivate); NULL keeps the current value. Nurseries can't change status.
UPDATE organizations
SET name   = COALESCE(sqlc.narg(name), name),
    status = COALESCE(sqlc.narg(status)::organization_status, status)
WHERE id = @id;

-- name: SearchUsers :many
-- Newest first; an empty term matches everyone.
SELECT * FROM users
WHERE @term::text = '' OR strpos(email, @term::text) > 0
ORDER BY created_at DESC
LIMIT 50;

-- name: ListMembershipsForUsers :many
SELECT m.user_id, o.id AS organization_id, o.name AS organization_name, m.role
FROM memberships m
JOIN organizations o ON o.id = m.organization_id
WHERE m.user_id = ANY(@user_ids::bigint[])
ORDER BY o.name;
