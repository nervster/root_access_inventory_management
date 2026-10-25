-- Choosing plants, step 2 (docs/plant-picker-storefront.md): shoppers choose the exact plants they buy from
-- their photos. The storefront knows each plant by its ID (never its barcode), and a cart holds the plants it
-- chose for 30 minutes while the shopper pays. At most one live hold per plant, enforced here, so two
-- shoppers choosing the same plant at the same moment can't both win.

-- +goose Up
CREATE TYPE plant_hold_status AS ENUM (
    'held',     -- a cart holds it until expires_at
    'ordered',  -- an order was imported for the plant, which holds it itself now (plants.status 'held')
    'expired',  -- the hold ran out (the cart can take it again if the plant is still free)
    'released'  -- the cart gave it back
);

CREATE TABLE plant_holds (
    id              bigint            GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint            NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_id        bigint            NOT NULL,
    status          plant_hold_status NOT NULL DEFAULT 'held',
    cart_token      text              NOT NULL,
    client_ip       text,             -- for the limit on holds per visitor
    expires_at      timestamptz       NOT NULL,
    created_at      timestamptz       NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, plant_id) REFERENCES plants (organization_id, id) ON DELETE CASCADE
);
-- The rule: one live hold per plant. (An expired hold isn't live: it's marked expired before the plant is
-- held again.)
CREATE UNIQUE INDEX plant_holds_one_per_plant ON plant_holds (organization_id, plant_id) WHERE status = 'held';
CREATE INDEX plant_holds_cart_idx ON plant_holds (organization_id, cart_token) WHERE status = 'held';
CREATE INDEX plant_holds_ip_idx ON plant_holds (organization_id, client_ip) WHERE status = 'held';

GRANT SELECT, INSERT, UPDATE, DELETE ON plant_holds TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE plant_holds ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plant_holds
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE plant_holds;
DROP TYPE plant_hold_status;
