-- Each product's price and details history (an audit log), built like plant_events (00011 to 00013):
-- Postgres triggers write an entry whenever a SKU's price or code changes, a SKU is added or deleted,
-- or the product's title, description, type or tags change, so every way of changing a product is
-- covered (including the SKUs created by marking plants for sale). Stock, photos and Shopify sync
-- status aren't recorded. Who did it comes from current_user_id() / current_user_name(), copied in
-- so names survive members leaving. SKU codes and pot size names are copied in as text, so entries
-- still read right after a SKU is deleted.

-- +goose Up
CREATE TYPE product_event_type AS ENUM ('price_changed', 'sku_added', 'sku_deleted', 'sku_changed', 'details_changed');

CREATE TABLE product_events (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint             NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    product_id      bigint             NOT NULL,
    type            product_event_type NOT NULL,
    -- price_changed: {"sku", "potSize", "from", "to"} in cents (null = no price)
    -- sku_added / sku_deleted: {"sku", "potSize", "priceCents"}
    -- sku_changed: {"potSize", "from", "to"} (the codes)
    -- details_changed: {"field": "title" | "description" | "productType" | "tags", "from", "to"}
    details         jsonb              NOT NULL DEFAULT '{}',
    changed_by      bigint             REFERENCES users (id) ON DELETE SET NULL,
    changed_by_name text,
    occurred_at     timestamptz        NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX product_events_product_idx ON product_events (product_id, occurred_at, id);

GRANT SELECT, INSERT ON product_events TO nms_tenant; -- history is never edited or deleted by the app
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE product_events ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_events
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose StatementBegin
CREATE FUNCTION record_product_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    field record;
BEGIN
    -- Creating the product (when plants are first marked for sale) isn't an event: its SKUs are.
    IF TG_OP = 'INSERT' THEN
        RETURN NEW;
    END IF;

    FOR field IN
        SELECT * FROM (VALUES
            ('title',       to_jsonb(OLD.title),        to_jsonb(NEW.title),        OLD.title        IS DISTINCT FROM NEW.title),
            ('description', to_jsonb(OLD.description),  to_jsonb(NEW.description),  OLD.description  IS DISTINCT FROM NEW.description),
            ('productType', to_jsonb(OLD.product_type), to_jsonb(NEW.product_type), OLD.product_type IS DISTINCT FROM NEW.product_type),
            ('tags',        to_jsonb(OLD.tags),         to_jsonb(NEW.tags),         OLD.tags         IS DISTINCT FROM NEW.tags)
        ) AS f (name, old_value, new_value, changed) WHERE changed
    LOOP
        INSERT INTO product_events (organization_id, product_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.id, 'details_changed',
                jsonb_build_object('field', field.name, 'from', field.old_value, 'to', field.new_value),
                current_user_id(), current_user_name());
    END LOOP;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER product_events_on_change
    AFTER INSERT OR UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION record_product_event();

-- +goose StatementBegin
CREATE FUNCTION record_product_variant_event() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    pot_size_name text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        -- Variants removed along with their whole product need no entry (its history goes too).
        IF NOT EXISTS (SELECT 1 FROM products WHERE id = OLD.product_id) THEN
            RETURN OLD;
        END IF;
        SELECT name INTO pot_size_name FROM pot_sizes WHERE id = OLD.pot_size_id;
        INSERT INTO product_events (organization_id, product_id, type, details, changed_by, changed_by_name)
        VALUES (OLD.organization_id, OLD.product_id, 'sku_deleted',
                jsonb_build_object('sku', OLD.sku, 'potSize', pot_size_name, 'priceCents', OLD.price_cents),
                current_user_id(), current_user_name());
        RETURN OLD;
    END IF;

    SELECT name INTO pot_size_name FROM pot_sizes WHERE id = NEW.pot_size_id;

    IF TG_OP = 'INSERT' THEN
        INSERT INTO product_events (organization_id, product_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.product_id, 'sku_added',
                jsonb_build_object('sku', NEW.sku, 'potSize', pot_size_name, 'priceCents', NEW.price_cents),
                current_user_id(), current_user_name());
        RETURN NEW;
    END IF;

    IF NEW.sku IS DISTINCT FROM OLD.sku THEN
        INSERT INTO product_events (organization_id, product_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.product_id, 'sku_changed',
                jsonb_build_object('potSize', pot_size_name, 'from', OLD.sku, 'to', NEW.sku),
                current_user_id(), current_user_name());
    END IF;

    IF NEW.price_cents IS DISTINCT FROM OLD.price_cents THEN
        INSERT INTO product_events (organization_id, product_id, type, details, changed_by, changed_by_name)
        VALUES (NEW.organization_id, NEW.product_id, 'price_changed',
                jsonb_build_object('sku', NEW.sku, 'potSize', pot_size_name, 'from', OLD.price_cents, 'to', NEW.price_cents),
                current_user_id(), current_user_name());
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER product_events_on_variant_change
    AFTER INSERT OR UPDATE OR DELETE ON product_variants
    FOR EACH ROW EXECUTE FUNCTION record_product_variant_event();

-- +goose Down
DROP TRIGGER product_events_on_variant_change ON product_variants;
DROP TRIGGER product_events_on_change ON products;
DROP FUNCTION record_product_variant_event();
DROP FUNCTION record_product_event();
DROP TABLE product_events;
DROP TYPE product_event_type;
