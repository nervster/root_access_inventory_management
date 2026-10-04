-- name: CreateMembership :one
INSERT INTO memberships (organization_id, user_id, role)
VALUES (@organization_id, @user_id, @role)
RETURNING *;

-- name: GetAccess :one
-- The user's role in an organization, and whether the organization is active.
SELECT m.id AS membership_id, m.role, o.status AS organization_status
FROM memberships m
JOIN organizations o ON o.id = m.organization_id
WHERE m.organization_id = @organization_id AND m.user_id = @user_id;

-- name: ListMembershipsForUser :many
SELECT o.id AS organization_id, o.name AS organization_name, o.slug, o.status, m.role
FROM memberships m
JOIN organizations o ON o.id = m.organization_id
WHERE m.user_id = @user_id
ORDER BY o.name;

-- name: ListMembers :many
-- Owners first (enum order: owner, admin, staff, viewer), then by email.
SELECT m.id, m.user_id, u.email, u.display_name, m.role, m.created_at
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.organization_id = @organization_id
ORDER BY m.role, u.email;

-- name: GetMember :one
SELECT m.id, m.user_id, u.email, u.display_name, m.role, m.created_at
FROM memberships m
JOIN users u ON u.id = m.user_id
WHERE m.organization_id = @organization_id AND m.id = @id;

-- name: UpdateMemberRole :exec
UPDATE memberships
SET role = @role
WHERE organization_id = @organization_id AND id = @id;

-- name: DeleteMember :exec
DELETE FROM memberships
WHERE organization_id = @organization_id AND id = @id;

-- name: CountOwners :one
SELECT count(*) FROM memberships
WHERE organization_id = @organization_id AND role = 'owner';
