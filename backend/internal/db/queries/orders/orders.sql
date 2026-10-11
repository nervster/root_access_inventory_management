-- Inside one nursery (run through db.InTenant) --

-- name: GetOrderByShopifyID :one
SELECT id, status FROM orders
WHERE organization_id = @organization_id AND shopify_order_id = @shopify_order_id;

-- name: CreateOrder :one
INSERT INTO orders (organization_id, shop, shopify_order_id, name, placed_at, shopify_fulfillment_status, shopify_updated_at)
VALUES (@organization_id, @shop, @shopify_order_id, @name, @placed_at, @shopify_fulfillment_status, @shopify_updated_at)
RETURNING id;

-- name: UpdateOrderFromShopify :exec
UPDATE orders
SET cancelled_at = @cancelled_at, shopify_fulfillment_status = @shopify_fulfillment_status, shopify_updated_at = @shopify_updated_at
WHERE organization_id = @organization_id AND id = @id;

-- name: SetOrderStatus :exec
UPDATE orders SET status = @status
WHERE organization_id = @organization_id AND id = @id;

-- name: CreateOrderItem :one
INSERT INTO order_items (organization_id, order_id, shopify_line_item_id, variant_id, sku, title, quantity, price_cents)
VALUES (@organization_id, @order_id, @shopify_line_item_id, @variant_id, @sku, @title, @quantity, @price_cents)
RETURNING id;

-- name: VariantForOrderItem :one
-- The Plant Stock SKU an order item is for: by its Shopify variant, else by SKU.
SELECT v.id
FROM product_variants v
LEFT JOIN shopify_variants sv ON sv.variant_id = v.id AND sv.shop = @shop
WHERE v.organization_id = @organization_id
  AND (sv.shopify_variant_id = sqlc.narg(shopify_variant_id) OR v.sku = sqlc.narg(sku))
ORDER BY (sv.shopify_variant_id = sqlc.narg(shopify_variant_id)) DESC NULLS LAST
LIMIT 1;

-- name: HoldPlants :many
-- Holds up to @quantity for-sale plants of the SKU for an order item, oldest first. SKIP LOCKED: two
-- imports at once never hold the same plant. Returns their barcodes (fewer when it's oversold).
-- The plants are chosen once, up front (MATERIALIZED): inside UPDATE … WHERE id IN (…), Postgres may
-- run the choice again as it goes, and LIMIT would no longer limit.
WITH chosen AS MATERIALIZED (
    SELECT candidate.id
    FROM plants candidate
    JOIN product_variants v ON v.id = @variant_id
    JOIN products pr ON pr.id = v.product_id
    WHERE candidate.organization_id = @organization_id
      AND candidate.status = 'for_sale'
      AND candidate.plant_name_id = pr.plant_name_id
      AND candidate.variegation_id IS NOT DISTINCT FROM pr.variegation_id
      AND candidate.pot_size_id = v.pot_size_id
    ORDER BY candidate.created_at, candidate.id
    LIMIT @quantity
    FOR UPDATE OF candidate SKIP LOCKED
)
UPDATE plants p
SET status = 'held', order_item_id = @order_item_id, updated_at = now()
FROM chosen
WHERE p.id = chosen.id
RETURNING p.barcode;

-- name: ReleaseOrder :many
-- A cancelled order's held plants go back on sale.
UPDATE plants
SET status = 'for_sale', order_item_id = NULL, updated_at = now()
WHERE plants.organization_id = @organization_id AND plants.status = 'held'
  AND plants.order_item_id IN (SELECT oi.id FROM order_items oi WHERE oi.order_id = @order_id)
RETURNING barcode;

-- name: SellOrder :many
-- An order handed over: its held plants are sold (they keep which order bought them).
UPDATE plants
SET status = 'sold', updated_at = now()
WHERE plants.organization_id = @organization_id AND plants.status = 'held'
  AND plants.order_item_id IN (SELECT oi.id FROM order_items oi WHERE oi.order_id = @order_id)
RETURNING barcode;

-- name: ListOrders :many
SELECT id, shop, shopify_order_id, name, status, placed_at, cancelled_at, shopify_fulfillment_status
FROM orders
WHERE organization_id = @organization_id AND status = @status
ORDER BY placed_at DESC, id DESC
LIMIT 200;

-- name: ListOrderItems :many
-- The orders' items, each with its pot size and the barcodes of the plants held for it.
SELECT i.id, i.order_id, i.title, i.sku, i.quantity, i.price_cents, i.variant_id, ps.name AS pot_size,
       coalesce(array_agg(p.barcode ORDER BY p.barcode) FILTER (WHERE p.id IS NOT NULL), '{}')::text[] AS held_barcodes
FROM order_items i
LEFT JOIN product_variants v ON v.id = i.variant_id
LEFT JOIN pot_sizes ps ON ps.id = v.pot_size_id
LEFT JOIN plants p ON p.order_item_id = i.id AND p.status = 'held'
WHERE i.organization_id = @organization_id AND i.order_id = ANY(@order_ids::bigint[])
GROUP BY i.id, ps.name
ORDER BY i.order_id, i.id;

-- name: SetOrdersChecked :exec
UPDATE shopify_connections SET orders_checked_through = @through, orders_error = NULL
WHERE organization_id = @organization_id;

-- name: SetOrdersError :exec
UPDATE shopify_connections SET orders_error = @orders_error
WHERE organization_id = @organization_id;

-- Account-level (the worker, every nursery) --

-- name: QueueOrderImports :exec
-- Every minute: check each connected store for new or changed orders.
INSERT INTO jobs (organization_id, kind, payload)
SELECT organization_id, 'shopify.import_orders', '{}'
FROM shopify_connections
WHERE NOT needs_reconnect
ON CONFLICT DO NOTHING;

-- name: ShopifyConnectionByShop :one
-- For webhooks, which name the store, not the nursery.
SELECT organization_id FROM shopify_connections WHERE shop = @shop;
