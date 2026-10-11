-- Each nursery's link to its Shopify store: one store per nursery, and a store belongs to one
-- nursery. An Owner connects it from Settings (OAuth, see internal/shopify).

-- +goose Up
CREATE TABLE shopify_connections (
    organization_id          bigint      PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    shop                     text        NOT NULL UNIQUE, -- e.g. root-access-htx.myshopify.com
    scopes                   text        NOT NULL,        -- what the store granted, comma-separated
    -- The API encrypts the tokens (AES-256-GCM, key SHOPIFY_TOKEN_KEY) before storing them, so a
    -- database copy alone can't reach anyone's store.
    access_token             bytea       NOT NULL,        -- lasts an hour
    access_token_expires_at  timestamptz NOT NULL,
    refresh_token            bytea       NOT NULL,        -- lasts 90 days; replaced on every refresh
    refresh_token_expires_at timestamptz NOT NULL,
    needs_reconnect          boolean     NOT NULL DEFAULT false, -- Shopify refused the tokens (e.g. app uninstalled)
    location_id              text,                        -- where customers pick up (a Shopify Location ID)
    location_name            text,
    connected_by             bigint      REFERENCES users (id) ON DELETE SET NULL,
    connected_by_name        text,                        -- copied in, like plant history (00013)
    connected_at             timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, UPDATE, DELETE ON shopify_connections TO nms_tenant;
ALTER TABLE shopify_connections ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shopify_connections
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- A connection in progress: made when an Owner clicks Connect, used up when Shopify sends them
-- back. The callback has no session token, so it finds the nursery by state (as the owner role).
CREATE TABLE shopify_oauth_states (
    state           text        PRIMARY KEY, -- random, also kept in the browser's cookie
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id         bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    shop            text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
GRANT SELECT, INSERT, DELETE ON shopify_oauth_states TO nms_tenant;
ALTER TABLE shopify_oauth_states ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shopify_oauth_states
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE shopify_oauth_states;
DROP TABLE shopify_connections;
