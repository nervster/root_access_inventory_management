-- Pushing stock to Shopify: each SKU's on-hand count at the pickup location is its number of
-- for-sale plants. A trigger queues a stock sync whenever a plant goes on or off sale, or a for-sale
-- plant changes pot or name, for nurseries with a connected store.

-- +goose Up
-- Where the SKU's stock is kept on Shopify (it has to be stocked at a location before its count can
-- be set). NULL until the first stock sync.
ALTER TABLE shopify_variants ADD COLUMN stocked_location_id text;

-- Queues a stock sync of the product that plants with this name and variegation belong to.
CREATE FUNCTION queue_shopify_stock_sync(org bigint, plant_name bigint, variegation bigint) RETURNS void
    LANGUAGE sql
    AS $$
        INSERT INTO jobs (organization_id, kind, payload)
        SELECT org, 'shopify.sync_stock', jsonb_build_object('productId', p.id)
        FROM products p
        WHERE p.organization_id = org
          AND p.plant_name_id = plant_name
          AND p.variegation_id IS NOT DISTINCT FROM variegation
          AND EXISTS (SELECT 1 FROM shopify_connections WHERE organization_id = org)
        ON CONFLICT DO NOTHING
    $$;

-- +goose StatementBegin
CREATE FUNCTION queue_shopify_stock_sync_on_plant_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.status = OLD.status AND NEW.pot_size_id = OLD.pot_size_id
       AND NEW.plant_name_id = OLD.plant_name_id AND NEW.variegation_id IS NOT DISTINCT FROM OLD.variegation_id THEN
        RETURN NEW; -- nothing that counts as stock changed
    END IF;
    IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.status = 'for_sale' THEN
        PERFORM queue_shopify_stock_sync(OLD.organization_id, OLD.plant_name_id, OLD.variegation_id);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND NEW.status = 'for_sale' THEN
        PERFORM queue_shopify_stock_sync(NEW.organization_id, NEW.plant_name_id, NEW.variegation_id);
    END IF;
    RETURN NULL; -- AFTER trigger: the result is ignored
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shopify_stock_on_plant_change
    AFTER INSERT OR DELETE OR UPDATE OF status, pot_size_id, plant_name_id, variegation_id ON plants
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_stock_sync_on_plant_change();

-- +goose Down
DROP TRIGGER shopify_stock_on_plant_change ON plants;
DROP FUNCTION queue_shopify_stock_sync_on_plant_change();
DROP FUNCTION queue_shopify_stock_sync(bigint, bigint, bigint);
ALTER TABLE shopify_variants DROP COLUMN stocked_location_id;
