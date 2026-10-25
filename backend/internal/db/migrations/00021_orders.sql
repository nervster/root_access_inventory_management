-- Online orders from Shopify, and the plants held for them until pickup. The worker imports orders
-- (internal/shopify/orders.go); for each item it holds that many for-sale plants of the SKU, oldest
-- first, so staff know exactly which plants to pull. Held plants still count as on hand on Shopify
-- (they're still here); Shopify subtracts them as committed until pickup.

-- +goose Up
CREATE TABLE orders (
    id                         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id            bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    shop                       text        NOT NULL,
    shopify_order_id           text        NOT NULL, -- gid://shopify/Order/123
    name                       text        NOT NULL, -- "#1001", as the customer sees it
    status                     text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'picked_up', 'cancelled')),
    placed_at                  timestamptz NOT NULL,
    cancelled_at               timestamptz,
    shopify_fulfillment_status text,                 -- as Shopify shows it, e.g. UNFULFILLED
    shopify_updated_at         timestamptz NOT NULL,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, shopify_order_id),      -- importing an order again updates it
    UNIQUE (organization_id, id)
);
CREATE INDEX orders_open_idx ON orders (organization_id, placed_at) WHERE status = 'open';

CREATE TABLE order_items (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id      bigint  NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    order_id             bigint  NOT NULL,
    shopify_line_item_id text    NOT NULL,
    variant_id           bigint,          -- the Plant Stock SKU; NULL for items that aren't Plant Stock's
    sku                  text,
    title                text    NOT NULL, -- as on the order
    quantity             integer NOT NULL CHECK (quantity > 0),
    price_cents          integer,          -- each, before discounts
    UNIQUE (order_id, shopify_line_item_id),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, order_id) REFERENCES orders (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, variant_id) REFERENCES product_variants (organization_id, id) ON DELETE SET NULL (variant_id)
);

-- Which order item a plant is held for (and, once picked up, which order bought it).
ALTER TABLE plants ADD COLUMN order_item_id bigint,
    ADD FOREIGN KEY (organization_id, order_item_id) REFERENCES order_items (organization_id, id) ON DELETE SET NULL (order_item_id),
    ADD CONSTRAINT plants_held_for_an_order CHECK (status <> 'held' OR order_item_id IS NOT NULL);
CREATE INDEX plants_order_item_idx ON plants (order_item_id) WHERE order_item_id IS NOT NULL;

-- Order importing: up to when orders have been read, and why the last import failed (for Settings).
ALTER TABLE shopify_connections
    ADD COLUMN orders_checked_through timestamptz,
    ADD COLUMN orders_error text;

GRANT SELECT, INSERT, UPDATE, DELETE ON orders, order_items TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE orders ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orders
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE order_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON order_items
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- History: being held for an order, and released from one, name the order.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    batch_code text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT code INTO batch_code FROM batches WHERE id = NEW.batch_id;
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'received',
                jsonb_build_object('batch', batch_code, 'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
        RETURN NEW;
    END IF;

    IF NEW.pot_size_id IS DISTINCT FROM OLD.pot_size_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'repotted',
                jsonb_build_object('fromPotSize', (SELECT name FROM pot_sizes WHERE id = OLD.pot_size_id),
                                   'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id,
                CASE
                    WHEN NEW.status = 'held' THEN 'held_for_order'
                    WHEN OLD.status = 'held' AND NEW.status = 'for_sale' THEN 'released_from_order'
                    WHEN NEW.status = 'for_sale' THEN 'marked_for_sale'
                    WHEN NEW.status = 'sold' THEN 'sold'
                    WHEN NEW.status = 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale' -- back to growing
                END::plant_event_type,
                CASE
                    WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                    WHEN NEW.status = 'held' OR OLD.status = 'held' THEN jsonb_strip_nulls(jsonb_build_object('order',
                        (SELECT o.name FROM order_items i JOIN orders o ON o.id = i.order_id
                         WHERE i.id = coalesce(NEW.order_item_id, OLD.order_item_id))))
                    ELSE '{}'::jsonb
                END,
                current_user_id(), current_user_name());
    END IF;

    IF NEW.plant_name_id IS DISTINCT FROM OLD.plant_name_id OR NEW.variegation_id IS DISTINCT FROM OLD.variegation_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'name_corrected',
                jsonb_build_object(
                    'from', concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = OLD.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = OLD.variegation_id)),
                    'to',   concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = NEW.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = NEW.variegation_id))),
                current_user_id(), current_user_name());
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Held plants count toward Shopify's on-hand stock too, so changes to them queue a stock sync.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION queue_shopify_stock_sync_on_plant_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status = OLD.status AND NEW.pot_size_id = OLD.pot_size_id
       AND NEW.plant_name_id = OLD.plant_name_id AND NEW.variegation_id IS NOT DISTINCT FROM OLD.variegation_id THEN
        RETURN NULL; -- nothing that counts as stock changed
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.status IN ('for_sale', 'held') THEN
        PERFORM queue_shopify_stock_sync(OLD.organization_id, OLD.plant_name_id, OLD.variegation_id);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND NEW.status IN ('for_sale', 'held') THEN
        PERFORM queue_shopify_stock_sync(NEW.organization_id, NEW.plant_name_id, NEW.variegation_id);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION queue_shopify_stock_sync_on_plant_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status = OLD.status AND NEW.pot_size_id = OLD.pot_size_id
       AND NEW.plant_name_id = OLD.plant_name_id AND NEW.variegation_id IS NOT DISTINCT FROM OLD.variegation_id THEN
        RETURN NEW;
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.status = 'for_sale' THEN
        PERFORM queue_shopify_stock_sync(OLD.organization_id, OLD.plant_name_id, OLD.variegation_id);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND NEW.status = 'for_sale' THEN
        PERFORM queue_shopify_stock_sync(NEW.organization_id, NEW.plant_name_id, NEW.variegation_id);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_plant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    batch_code text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        SELECT code INTO batch_code FROM batches WHERE id = NEW.batch_id;
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'received',
                jsonb_build_object('batch', batch_code, 'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
        RETURN NEW;
    END IF;

    IF NEW.pot_size_id IS DISTINCT FROM OLD.pot_size_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'repotted',
                jsonb_build_object('fromPotSize', (SELECT name FROM pot_sizes WHERE id = OLD.pot_size_id),
                                   'toPotSize', (SELECT name FROM pot_sizes WHERE id = NEW.pot_size_id)),
                current_user_id(), current_user_name());
    END IF;

    IF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id,
                CASE NEW.status
                    WHEN 'for_sale' THEN 'marked_for_sale'
                    WHEN 'sold' THEN 'sold'
                    WHEN 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale'
                END::plant_event_type,
                CASE WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                     ELSE '{}'::jsonb END,
                current_user_id(), current_user_name());
    END IF;

    IF NEW.plant_name_id IS DISTINCT FROM OLD.plant_name_id OR NEW.variegation_id IS DISTINCT FROM OLD.variegation_id THEN
        INSERT INTO plant_events (organization_id, plant_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'name_corrected',
                jsonb_build_object(
                    'from', concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = OLD.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = OLD.variegation_id)),
                    'to',   concat_ws(' ', (SELECT full_name FROM plant_names WHERE id = NEW.plant_name_id),
                                           (SELECT name FROM variegations WHERE id = NEW.variegation_id))),
                current_user_id(), current_user_name());
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
UPDATE plants SET status = 'for_sale' WHERE status = 'held';
ALTER TABLE shopify_connections DROP COLUMN orders_error, DROP COLUMN orders_checked_through;
DROP INDEX plants_order_item_idx;
ALTER TABLE plants DROP CONSTRAINT plants_held_for_an_order, DROP COLUMN order_item_id;
DROP TABLE order_items;
DROP TABLE orders;
