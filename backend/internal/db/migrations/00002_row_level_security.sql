-- Keeps every nursery's data apart, enforced by Postgres itself (Row-Level Security).
--
-- Work for one nursery runs as the restricted role nms_tenant, with the nursery's ID in the
-- setting app.organization_id (Go: db.InTenant). Policies then hide every row of other nurseries,
-- even if a query forgets to filter. Account-level work (sign-in, "which nurseries am I in?",
-- platform admin) runs as the owner role, which RLS doesn't restrict.
--
-- Rules for future migrations:
--   * Nursery-owned table: organization_id column, ENABLE ROW LEVEL SECURITY, a tenant_isolation
--     policy like the ones below, and GRANTs to nms_tenant. (A test fails if RLS is missing.)
--   * Shared reference table (e.g. plant categories): no organization_id; GRANT SELECT to nms_tenant.

-- +goose Up
-- Roles belong to the whole Postgres server, so another database (e.g. nms_test) may have made it.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'nms_tenant') THEN
        CREATE ROLE nms_tenant NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd
GRANT nms_tenant TO CURRENT_USER; -- lets the app's login role switch to it (SET ROLE)

-- The nursery the current transaction works for; NULL when none is set, which matches no rows.
CREATE FUNCTION current_organization_id() RETURNS bigint
    LANGUAGE sql STABLE
    AS $$ SELECT NULLIF(current_setting('app.organization_id', true), '')::bigint $$;

GRANT USAGE ON SCHEMA public TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant; -- for inserts into identity columns

-- organizations: a nursery sees only itself, and may change only its settings (not its status).
GRANT SELECT ON organizations TO nms_tenant;
GRANT UPDATE (name, time_zone) ON organizations TO nms_tenant;
ALTER TABLE organizations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON organizations
    USING (id = current_organization_id());

-- memberships and invitations: nursery-owned.
GRANT SELECT, INSERT, UPDATE, DELETE ON memberships, invitations TO nms_tenant;
ALTER TABLE memberships ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON memberships
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invitations
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- users: shared across nurseries (one person can be in several), so a nursery may read only
-- its own members. The memberships subquery is itself limited to the current nursery.
GRANT SELECT ON users TO nms_tenant;
ALTER TABLE users ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_members ON users FOR SELECT
    USING (EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = users.id));

-- +goose Down
DROP POLICY tenant_members ON users;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON invitations;
ALTER TABLE invitations DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON memberships;
ALTER TABLE memberships DISABLE ROW LEVEL SECURITY;
DROP POLICY tenant_isolation ON organizations;
ALTER TABLE organizations DISABLE ROW LEVEL SECURITY;
REVOKE ALL ON users, organizations, memberships, invitations FROM nms_tenant;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM nms_tenant;
REVOKE USAGE ON SCHEMA public FROM nms_tenant;
DROP FUNCTION current_organization_id();
-- The nms_tenant role stays: other databases on the server may still use it.
