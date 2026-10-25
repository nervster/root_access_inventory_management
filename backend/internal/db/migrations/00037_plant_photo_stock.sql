-- Choosing plants, step 4 (docs/plant-picker-storefront.md): shoppers choose plants by their photos, so a
-- for-sale plant counts as stock on Shopify only once it has a photo (StockToSync, DriftExpected). Adding a
-- for-sale plant's first photo, or deleting its last, changes its SKU's stock: queue a stock sync, as plant
-- changes do (00018).

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION queue_shopify_stock_sync_on_photo_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    photo plant_photos;
BEGIN
    IF TG_OP = 'DELETE' THEN
        photo := OLD;
    ELSE
        photo := NEW;
    END IF;
    PERFORM queue_shopify_stock_sync(p.organization_id, p.plant_name_id, p.variegation_id)
    FROM plants p
    WHERE p.organization_id = photo.organization_id AND p.id = photo.plant_id AND p.status = 'for_sale';
    RETURN NULL; -- AFTER trigger: the result is ignored
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shopify_stock_on_photo_change
    AFTER INSERT OR DELETE ON plant_photos
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_stock_sync_on_photo_change();

-- Stores already connected count only photographed plants from now on: send every product's stock again.
INSERT INTO jobs (organization_id, kind, payload)
SELECT sp.organization_id, 'shopify.sync_stock', jsonb_build_object('productId', sp.product_id)
FROM shopify_products sp
ON CONFLICT DO NOTHING;

-- +goose Down
DROP TRIGGER shopify_stock_on_photo_change ON plant_photos;
DROP FUNCTION queue_shopify_stock_sync_on_photo_change();
