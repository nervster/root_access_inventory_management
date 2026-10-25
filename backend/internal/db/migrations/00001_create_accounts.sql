-- Accounts: who signs in (users), the nurseries using NMS (organizations), who belongs to which
-- nursery with what role (memberships), and pending invites to join one (invitations).

-- +goose Up
CREATE TYPE org_role AS ENUM ('owner', 'admin', 'staff', 'viewer');
CREATE TYPE organization_status AS ENUM ('active', 'suspended');

-- A person who signs in through Clerk. Created on their first authenticated request.
CREATE TABLE users (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    external_id  text        NOT NULL UNIQUE, -- Clerk user ID (the session token's "sub" claim)
    email        text        NOT NULL,       -- lower-cased, kept in sync from the session token
    display_name text,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX users_email_idx ON users (email);

-- A nursery using NMS (a tenant). Everything tenant-owned has an organization_id.
CREATE TABLE organizations (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text                NOT NULL,
    slug       text                NOT NULL UNIQUE, -- URL-friendly, e.g. "root-access-htx"
    time_zone  text                NOT NULL DEFAULT 'America/Chicago', -- IANA name, used for reports
    status     organization_status NOT NULL DEFAULT 'active',
    created_at timestamptz         NOT NULL DEFAULT now()
);

-- A user's role in one organization. The same user can have different roles in different organizations.
CREATE TABLE memberships (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            org_role    NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, user_id)
);
CREATE INDEX memberships_user_id_idx ON memberships (user_id);

-- An invite for an email address to join an organization with a role.
CREATE TABLE invitations (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id    bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    email              text        NOT NULL, -- lower-cased
    role               org_role    NOT NULL,
    invited_by_user_id bigint REFERENCES users (id) ON DELETE SET NULL, -- a member or platform admin; null if deleted
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    accepted_at        timestamptz,
    revoked_at         timestamptz
);
-- At most one open invite per email per organization.
CREATE UNIQUE INDEX invitations_open_email_idx ON invitations (organization_id, email)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
CREATE INDEX invitations_email_idx ON invitations (email);

-- +goose Down
DROP TABLE invitations;
DROP TABLE memberships;
DROP TABLE organizations;
DROP TABLE users;
DROP TYPE organization_status;
DROP TYPE org_role;
