-- Pushing products to Shopify (inside one nursery, run through db.InTenant) --

-- name: LockProductSync :exec
-- One sync of a product at a time (until the transaction ends), so two can't both create it.
SELECT pg_advisory_xact_lock(hashtext('shopify.sync_product'), mod(@product_id::bigint, 2147483647)::integer);

-- name: ProductToSync :one
SELECT p.id, p.title, p.description, p.product_type, p.tags,
       sp.shopify_product_id
FROM products p
LEFT JOIN shopify_products sp ON sp.product_id = p.id AND sp.shop = @shop
WHERE p.organization_id = @organization_id AND p.id = @product_id;

-- name: VariantsToSync :many
-- The product's variants, smallest pot first, with their Shopify IDs if already pushed.
SELECT v.id, v.sku, v.price_cents, ps.name AS pot_size,
       sv.shopify_variant_id
FROM product_variants v
JOIN pot_sizes ps ON ps.id = v.pot_size_id
LEFT JOIN shopify_variants sv ON sv.variant_id = v.id AND sv.shop = @shop
WHERE v.organization_id = @organization_id AND v.product_id = @product_id
ORDER BY ps.sort_order, ps.name;

-- name: SaveShopifyProduct :exec
INSERT INTO shopify_products (organization_id, product_id, shop, shopify_product_id, synced_at, sync_error)
VALUES (@organization_id, @product_id, @shop, @shopify_product_id, now(), NULL)
ON CONFLICT (product_id, shop) DO UPDATE
SET shopify_product_id = excluded.shopify_product_id, synced_at = now(), sync_error = NULL;

-- name: SaveShopifySyncError :exec
INSERT INTO shopify_products (organization_id, product_id, shop, sync_error)
VALUES (@organization_id, @product_id, @shop, @sync_error)
ON CONFLICT (product_id, shop) DO UPDATE
SET sync_error = excluded.sync_error;

-- name: ForgetShopifyProduct :exec
-- The product was deleted in Shopify; the next sync creates it again.
DELETE FROM shopify_products WHERE product_id = @product_id AND shop = @shop;

-- name: ForgetShopifyVariants :exec
DELETE FROM shopify_variants
WHERE shop = @shop
  AND variant_id IN (SELECT id FROM product_variants WHERE product_id = @product_id);

-- name: SaveShopifyVariant :exec
INSERT INTO shopify_variants (organization_id, variant_id, shop, shopify_variant_id, shopify_inventory_item_id)
VALUES (@organization_id, @variant_id, @shop, @shopify_variant_id, @shopify_inventory_item_id);

-- name: ProductsToSync :many
-- Every product worth pushing: with a price, or already on Shopify (to update or take off sale).
SELECT p.id
FROM products p
WHERE p.organization_id = @organization_id
  AND (EXISTS (SELECT 1 FROM product_variants v WHERE v.product_id = p.id AND v.price_cents IS NOT NULL)
       OR EXISTS (SELECT 1 FROM shopify_products sp WHERE sp.product_id = p.id AND sp.shop = @shop))
ORDER BY p.id;

-- name: ListShopifyProducts :many
-- How each product stands on the store, for Settings.
SELECT p.id AS product_id, p.title, sp.shopify_product_id, sp.synced_at, sp.sync_error
FROM shopify_products sp
JOIN products p ON p.id = sp.product_id
WHERE sp.organization_id = @organization_id AND sp.shop = @shop
ORDER BY sp.sync_error IS NULL, p.title;
