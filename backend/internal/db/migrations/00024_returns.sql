-- Returns from Shopify (Shopify Returns): a customer asks to send plants back, staff receive them
-- (a refund only once the plant is physically back), check them, and the refund is issued in
-- Shopify within a day of receiving. The worker imports returns with orders (internal/shopify).

-- +goose Up
CREATE TABLE returns (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id   bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    order_id          bigint      NOT NULL,
    shopify_return_id text        NOT NULL, -- gid://shopify/Return/123
    name              text        NOT NULL, -- "#1001-R1"
    shopify_status    text        NOT NULL, -- REQUESTED, OPEN, CLOSED, DECLINED, CANCELED
    requested_at      timestamptz NOT NULL,
    received_at       timestamptz,          -- the first plant came back: the refund is due a day later
    refunded          boolean     NOT NULL DEFAULT false, -- every returned item refunded in Shopify
    UNIQUE (organization_id, shopify_return_id),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, order_id) REFERENCES orders (organization_id, id) ON DELETE CASCADE
);

CREATE TABLE return_items (
    id                          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id             bigint  NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    return_id                   bigint  NOT NULL,
    shopify_return_line_item_id text    NOT NULL,
    order_item_id               bigint,           -- NULL for items that aren't Plant Stock's
    quantity                    integer NOT NULL CHECK (quantity > 0),
    refunded_quantity           integer NOT NULL DEFAULT 0,
    reason                      text,             -- e.g. "Damaged"
    customer_note               text,
    UNIQUE (return_id, shopify_return_line_item_id),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, return_id) REFERENCES returns (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, order_item_id) REFERENCES order_items (organization_id, id) ON DELETE SET NULL (order_item_id)
);

-- Which return a plant came back with (kept after its check, for its history).
ALTER TABLE plants ADD COLUMN return_item_id bigint,
    ADD FOREIGN KEY (organization_id, return_item_id) REFERENCES return_items (organization_id, id) ON DELETE SET NULL (return_item_id),
    ADD CONSTRAINT plants_returned_with_a_return CHECK (status <> 'returned' OR return_item_id IS NOT NULL);
CREATE INDEX plants_return_item_idx ON plants (return_item_id) WHERE return_item_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON returns, return_items TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE returns ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON returns
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE return_items ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON return_items
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- History: a plant coming back names the return; its check is the usual for-sale/growing/discarded.
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
                    WHEN NEW.status = 'returned' THEN 'returned'
                    WHEN OLD.status = 'held' AND NEW.status = 'for_sale' THEN 'released_from_order'
                    WHEN NEW.status = 'for_sale' THEN 'marked_for_sale'
                    WHEN NEW.status = 'sold' THEN 'sold'
                    WHEN NEW.status = 'discarded' THEN 'discarded'
                    ELSE 'unmarked_for_sale' -- back to growing
                END::plant_event_type,
                CASE
                    WHEN NEW.status = 'discarded' THEN jsonb_strip_nulls(jsonb_build_object('reason', NEW.discard_reason))
                    WHEN NEW.status = 'returned' THEN jsonb_strip_nulls(jsonb_build_object('return',
                        (SELECT r.name FROM return_items ri JOIN returns r ON r.id = ri.return_id WHERE ri.id = NEW.return_item_id)))
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

-- +goose Down
-- The history trigger as 00021 left it.
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
                    ELSE 'unmarked_for_sale'
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
UPDATE plants SET status = 'sold' WHERE status = 'returned';
DROP INDEX plants_return_item_idx;
ALTER TABLE plants DROP CONSTRAINT plants_returned_with_a_return, DROP COLUMN return_item_id;
DROP TABLE return_items;
DROP TABLE returns;
