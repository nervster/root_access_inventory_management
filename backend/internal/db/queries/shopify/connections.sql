-- Inside one nursery (run through db.InTenant) --

-- name: GetShopifyConnection :one
SELECT * FROM shopify_connections
WHERE organization_id = @organization_id;

-- name: LockShopifyConnection :one
-- Holds the row until the transaction ends, so only one request at a time refreshes the tokens.
SELECT * FROM shopify_connections
WHERE organization_id = @organization_id
FOR UPDATE;

-- name: SaveShopifyConnection :one
-- Connecting again (same store, e.g. after uninstalling) keeps the chosen location.
INSERT INTO shopify_connections (organization_id, shop, scopes, access_token, access_token_expires_at,
                                 refresh_token, refresh_token_expires_at, connected_by, connected_by_name)
VALUES (@organization_id, @shop, @scopes, @access_token, @access_token_expires_at,
        @refresh_token, @refresh_token_expires_at, current_user_id(), current_user_name())
ON CONFLICT (organization_id) DO UPDATE
SET shop                     = excluded.shop,
    scopes                   = excluded.scopes,
    access_token             = excluded.access_token,
    access_token_expires_at  = excluded.access_token_expires_at,
    refresh_token            = excluded.refresh_token,
    refresh_token_expires_at = excluded.refresh_token_expires_at,
    needs_reconnect          = false,
    location_id              = CASE WHEN shopify_connections.shop = excluded.shop THEN shopify_connections.location_id END,
    location_name            = CASE WHEN shopify_connections.shop = excluded.shop THEN shopify_connections.location_name END,
    connected_by             = excluded.connected_by,
    connected_by_name        = excluded.connected_by_name,
    connected_at             = now()
RETURNING *;

-- name: UpdateShopifyTokens :exec
UPDATE shopify_connections
SET access_token = @access_token, access_token_expires_at = @access_token_expires_at,
    refresh_token = @refresh_token, refresh_token_expires_at = @refresh_token_expires_at
WHERE organization_id = @organization_id;

-- name: MarkShopifyNeedsReconnect :exec
UPDATE shopify_connections
SET needs_reconnect = true
WHERE organization_id = @organization_id;

-- name: SetShopifyLocation :exec
UPDATE shopify_connections
SET location_id = @location_id, location_name = @location_name
WHERE organization_id = @organization_id;

-- name: DeleteShopifyConnection :execrows
DELETE FROM shopify_connections
WHERE organization_id = @organization_id;

-- name: CreateShopifyOAuthState :exec
INSERT INTO shopify_oauth_states (state, organization_id, user_id, shop)
VALUES (@state, @organization_id, current_user_id(), @shop);

-- Account-level: the callback doesn't know the nursery yet --

-- name: TakeShopifyOAuthState :one
-- Uses up a connection in progress, so it works only once.
DELETE FROM shopify_oauth_states
WHERE state = @state
RETURNING *;

-- name: DeleteOldShopifyOAuthStates :exec
-- Connections started but never finished.
DELETE FROM shopify_oauth_states
WHERE created_at < now() - interval '1 hour';
