-- Inside one nursery (run through db.InTenant) --

-- name: GetOrderByShopifyID :one
SELECT id, status, cancelled_at FROM orders
WHERE organization_id = @organization_id AND shopify_order_id = @shopify_order_id;

-- name: CreateOrder :one
INSERT INTO orders (organization_id, shop, shopify_order_id, name, placed_at, shopify_fulfillment_status, shopify_updated_at)
VALUES (@organization_id, @shop, @shopify_order_id, @name, @placed_at, @shopify_fulfillment_status, @shopify_updated_at)
RETURNING id;

-- name: UpdateOrderFromShopify :exec
UPDATE orders
SET cancelled_at = @cancelled_at, shopify_fulfillment_status = @shopify_fulfillment_status, shopify_updated_at = @shopify_updated_at
WHERE organization_id = @organization_id AND id = @id;

-- name: OrderProductIDs :many
-- The Plant Stock products an order's items are for.
SELECT DISTINCT v.product_id
FROM order_items i
JOIN product_variants v ON v.id = i.variant_id
WHERE i.organization_id = @organization_id AND i.order_id = @order_id
ORDER BY v.product_id;

-- name: SetOrderStatus :exec
UPDATE orders SET status = @status
WHERE organization_id = @organization_id AND id = @id;

-- name: CreateOrderItem :one
-- chosen_plant_id comes from the storefront (the line's _plant): kept only if it's one of this nursery's plants.
INSERT INTO order_items (organization_id, order_id, shopify_line_item_id, variant_id, sku, title, quantity, price_cents,
                         chosen_plant_id)
VALUES (@organization_id, @order_id, @shopify_line_item_id, @variant_id, @sku, @title, @quantity, @price_cents,
        (SELECT p.id FROM plants p WHERE p.organization_id = @organization_id AND p.id = sqlc.narg(chosen_plant_id)))
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

-- name: HoldChosenPlant :many
-- Holds the plant the shopper chose for an order item, if it's still for sale and is the item's SKU (its name,
-- variegation and pot). Returns its barcode, or nothing when it can't be held.
UPDATE plants p
SET status = 'held', order_item_id = @order_item_id, updated_at = now()
FROM product_variants v
JOIN products pr ON pr.id = v.product_id
WHERE v.id = @variant_id
  AND p.organization_id = @organization_id AND p.id = @plant_id
  AND p.status = 'for_sale'
  AND p.plant_name_id = pr.plant_name_id
  AND p.variegation_id IS NOT DISTINCT FROM pr.variegation_id
  AND p.pot_size_id = v.pot_size_id
RETURNING p.barcode;

-- name: EndPlantHolds :exec
-- The plant was ordered: its cart hold (whichever cart's it is now) has done its job.
UPDATE plant_holds SET status = 'ordered'
WHERE organization_id = @organization_id AND plant_id = @plant_id AND status = 'held';

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

-- name: MarkOrderReady :one
-- In the locker (or set aside for an in-person pickup).
UPDATE orders
SET status = 'ready_for_pickup', ready_at = now(), ready_by_name = current_user_name()
WHERE organization_id = @organization_id AND id = @id AND status = 'open'
RETURNING id;

-- name: MarkOrderNotReady :one
-- Back to "to prepare" (e.g. marked ready by mistake). Nothing else changed when it was marked ready.
UPDATE orders
SET status = 'open', ready_at = NULL, ready_by_name = NULL
WHERE organization_id = @organization_id AND id = @id AND status = 'ready_for_pickup'
RETURNING id;

-- name: MarkOrderPickedUp :one
-- From ready, or straight from open (a bigger order handed over in person). The worker then fulfills
-- it in Shopify and sells its plants (FulfillOrder).
UPDATE orders
SET status = 'picked_up', picked_up_at = now(), picked_up_by_name = current_user_name()
WHERE organization_id = @organization_id AND id = @id AND status IN ('open', 'ready_for_pickup')
RETURNING id;

-- name: GetOrder :one
SELECT * FROM orders
WHERE organization_id = @organization_id AND id = @id;

-- name: ListOrders :many
-- Orders still to collect come in the order staff work through them: those with no booked slot first
-- (they need sorting out), then by ready by (the start of the booked window). The rest, newest first.
SELECT o.id, o.shop, o.shopify_order_id, o.name, o.status, o.placed_at, o.cancelled_at, o.shopify_fulfillment_status,
       o.ready_at, o.ready_by_name, o.picked_up_at, o.picked_up_by_name,
       o.pickup_date, o.pickup_window, o.pickup_note, o.pickup_problem, o.pickup_changed_at, o.locker_code,
       b.starts_at AS ready_by
FROM orders o
LEFT JOIN pickup_bookings b ON b.organization_id = o.organization_id AND b.order_id = o.id AND b.status = 'booked'
WHERE o.organization_id = @organization_id AND o.status = @status
ORDER BY CASE WHEN o.status IN ('open', 'ready_for_pickup') THEN b.starts_at END ASC NULLS FIRST,
         o.placed_at DESC, o.id DESC
LIMIT 200;

-- name: ListOrderItems :many
-- The orders' items, each with its pot size and the barcodes of its plants (held, or sold once picked up).
-- chosen_barcode: the plant the shopper chose from its photo, if they chose one.
SELECT i.id, i.order_id, i.title, i.sku, i.quantity, i.price_cents, i.variant_id, ps.name AS pot_size,
       coalesce(array_agg(p.barcode ORDER BY p.barcode) FILTER (WHERE p.id IS NOT NULL), '{}')::text[] AS held_barcodes,
       chosen.barcode AS chosen_barcode
FROM order_items i
LEFT JOIN product_variants v ON v.id = i.variant_id
LEFT JOIN pot_sizes ps ON ps.id = v.pot_size_id
LEFT JOIN plants p ON p.order_item_id = i.id AND p.status IN ('held', 'sold')
LEFT JOIN plants chosen ON chosen.organization_id = i.organization_id AND chosen.id = i.chosen_plant_id
WHERE i.organization_id = @organization_id AND i.order_id = ANY(@order_ids::bigint[])
GROUP BY i.id, ps.name, chosen.barcode
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

-- name: OrganizationsWithOrdersFrom :many
-- For Shopify's privacy requests, which name the store: the nurseries that have its orders (even
-- after disconnecting it).
SELECT DISTINCT organization_id FROM orders WHERE shop = @shop;

-- name: SetOrderPickup :exec
-- The pickup the order's cart chose (its attributes), and why its booking needs staff, if it does.
UPDATE orders
SET pickup_date = sqlc.narg(pickup_date), pickup_window = sqlc.narg(pickup_window),
    pickup_note = sqlc.narg(pickup_note), pickup_problem = sqlc.narg(pickup_problem)
WHERE organization_id = @organization_id AND id = @id;

-- name: GetOrderByName :one
-- For the store's My pickup page: the customer types the order number ("#1042").
SELECT * FROM orders WHERE organization_id = @organization_id AND name = @name;

-- name: SetOrderPickupChanged :exec
-- The customer moved the pickup to another slot.
UPDATE orders
SET pickup_date = @pickup_date, pickup_window = @pickup_window, pickup_problem = NULL, pickup_changed_at = @changed_at
WHERE organization_id = @organization_id AND id = @id;

-- name: SetOrderPickupSlot :exec
-- Staff moved the pickup to another slot: that's its time now, and any pickup problem is sorted.
UPDATE orders SET pickup_date = @pickup_date, pickup_window = @pickup_window, pickup_problem = NULL
WHERE organization_id = @organization_id AND id = @id;
