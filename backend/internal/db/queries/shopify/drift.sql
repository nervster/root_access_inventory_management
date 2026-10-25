-- The nightly drift check --

-- name: QueueDriftChecks :exec
-- Account-level (the worker, every few minutes): a check for each store where it's 3 am in the
-- nursery's time zone and none has run in the last 20 hours.
INSERT INTO jobs (organization_id, kind, payload)
SELECT c.organization_id, 'shopify.check_drift', '{}'
FROM shopify_connections c
JOIN organizations o ON o.id = c.organization_id
WHERE NOT c.needs_reconnect
  AND extract(hour FROM now() AT TIME ZONE o.time_zone) = 3
  AND (c.drift_checked_at IS NULL OR c.drift_checked_at < now() - interval '20 hours')
ON CONFLICT DO NOTHING;

-- Inside one nursery (run through db.InTenant) --

-- name: StartDriftCheck :exec
UPDATE shopify_connections SET drift_checked_at = now()
WHERE organization_id = @organization_id;

-- name: SaveDriftDifferences :exec
UPDATE shopify_connections SET drift_differences = @differences
WHERE organization_id = @organization_id;

-- name: DriftExpected :many
-- What the store should show: every product that's on it, or should be (it has a price), with each
-- SKU's price (NULL: should be off the store) and stock (for-sale plants with a photo, and held ones), as
-- StockToSync counts it.
SELECT p.id AS product_id, p.title, sp.shopify_product_id,
       v.sku, v.price_cents, sv.stocked_location_id,
       (SELECT count(*) FROM plants pl
        WHERE pl.organization_id = p.organization_id
          AND pl.plant_name_id = p.plant_name_id
          AND pl.variegation_id IS NOT DISTINCT FROM p.variegation_id
          AND pl.pot_size_id = v.pot_size_id
          AND (pl.status = 'held'
               OR pl.status = 'for_sale' AND EXISTS (SELECT 1 FROM plant_photos ph
                                                     WHERE ph.organization_id = pl.organization_id AND ph.plant_id = pl.id)))::integer AS stock
FROM products p
JOIN product_variants v ON v.product_id = p.id
LEFT JOIN shopify_products sp ON sp.product_id = p.id AND sp.shop = @shop
LEFT JOIN shopify_variants sv ON sv.variant_id = v.id AND sv.shop = @shop
WHERE p.organization_id = @organization_id
  AND (sp.shopify_product_id IS NOT NULL
       OR EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id = p.id AND pv.price_cents IS NOT NULL))
ORDER BY p.id, v.id;
