-- Inside one nursery (run through db.InTenant) --

-- name: CreateInvitation :one
INSERT INTO invitations (organization_id, email, role, invited_by_user_id, expires_at)
VALUES (@organization_id, @email, @role, @invited_by_user_id, @expires_at)
RETURNING *;

-- name: ListOpenInvitations :many
-- Not yet accepted or revoked (expired ones included, so they can be resent).
SELECT * FROM invitations
WHERE organization_id = @organization_id AND accepted_at IS NULL AND revoked_at IS NULL
ORDER BY created_at DESC;

-- name: GetOpenInvitation :one
SELECT * FROM invitations
WHERE organization_id = @organization_id AND id = @id AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: GetOpenInvitationByEmail :one
SELECT * FROM invitations
WHERE organization_id = @organization_id AND email = @email AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: ExtendInvitation :one
UPDATE invitations
SET expires_at = @expires_at
WHERE organization_id = @organization_id AND id = @id
RETURNING *;

-- name: RevokeInvitation :exec
UPDATE invitations
SET revoked_at = now()
WHERE organization_id = @organization_id AND id = @id AND accepted_at IS NULL AND revoked_at IS NULL;

-- name: AcceptInvitation :execrows
-- Returns 0 rows when the invitation is no longer pending (accepted, revoked, or expired).
UPDATE invitations
SET accepted_at = now()
WHERE organization_id = @organization_id AND id = @id
  AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: IsMemberByEmail :one
SELECT EXISTS (
    SELECT 1 FROM memberships m
    JOIN users u ON u.id = m.user_id
    WHERE m.organization_id = @organization_id AND u.email = @email
);

-- name: JoinOrganization :exec
-- Adds the membership; someone who is already a member keeps their current role.
INSERT INTO memberships (organization_id, user_id, role)
VALUES (@organization_id, @user_id, @role)
ON CONFLICT (organization_id, user_id) DO NOTHING;

-- Account level: invitations addressed to one person, across nurseries --

-- name: ListPendingInvitationsForEmail :many
SELECT i.id, i.organization_id, o.name AS organization_name, i.role, i.expires_at
FROM invitations i
JOIN organizations o ON o.id = i.organization_id
WHERE i.email = @email AND i.accepted_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > now()
  AND o.status = 'active'
ORDER BY i.created_at;

-- name: GetInvitationForEmail :one
SELECT i.id, i.organization_id, i.role, o.status AS organization_status
FROM invitations i
JOIN organizations o ON o.id = i.organization_id
WHERE i.id = @id AND i.email = @email;
