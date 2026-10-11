-- Returns (inside one nursery, run through db.InTenant) --

-- name: UpsertReturn :one
INSERT INTO returns (organization_id, order_id, shopify_return_id, name, shopify_status, requested_at)
VALUES (@organization_id, @order_id, @shopify_return_id, @name, @shopify_status, @requested_at)
ON CONFLICT (organization_id, shopify_return_id) DO UPDATE
SET name = excluded.name, shopify_status = excluded.shopify_status
RETURNING id;

-- name: UpsertReturnItem :exec
INSERT INTO return_items (organization_id, return_id, shopify_return_line_item_id, order_item_id, quantity, refunded_quantity, reason, customer_note)
VALUES (@organization_id, @return_id, @shopify_return_line_item_id, @order_item_id, @quantity, @refunded_quantity, @reason, @customer_note)
ON CONFLICT (return_id, shopify_return_line_item_id) DO UPDATE
SET quantity = excluded.quantity, refunded_quantity = excluded.refunded_quantity,
    reason = excluded.reason, customer_note = excluded.customer_note;

-- name: OrderItemByShopifyLineItem :one
SELECT id FROM order_items
WHERE organization_id = @organization_id AND order_id = @order_id AND shopify_line_item_id = @shopify_line_item_id;

-- name: SetReturnRefunded :exec
-- Refunded once Shopify shows every item refunded.
UPDATE returns r
SET refunded = NOT EXISTS (SELECT 1 FROM return_items ri WHERE ri.return_id = r.id AND ri.refunded_quantity < ri.quantity)
WHERE r.organization_id = @organization_id AND r.id = @id;

-- name: ReturnItemForPlant :one
-- The item of the return that a sold plant came back as: one for the plant's order item, with room
-- for another plant.
SELECT ri.id
FROM return_items ri
JOIN plants p ON p.order_item_id = ri.order_item_id
WHERE ri.organization_id = @organization_id AND ri.return_id = @return_id
  AND p.barcode = @barcode AND p.status = 'sold'
  AND (SELECT count(*) FROM plants received WHERE received.return_item_id = ri.id) < ri.quantity
ORDER BY ri.id
LIMIT 1;

-- name: ReceiveReturnedPlant :exec
UPDATE plants
SET status = 'returned', return_item_id = @return_item_id, updated_at = now()
WHERE organization_id = @organization_id AND barcode = @barcode AND status = 'sold';

-- name: MarkReturnReceived :exec
-- The first plant back starts the day the refund is due in.
UPDATE returns SET received_at = coalesce(received_at, now())
WHERE organization_id = @organization_id AND id = @id;

-- name: CheckReturnedPlant :one
-- After its check, a returned plant goes back on sale, back to growing, or out.
UPDATE plants
SET status = @status::plant_status, order_item_id = NULL, updated_at = now(),
    discarded_at = CASE WHEN @status::plant_status = 'discarded' THEN now() END,
    discard_reason = CASE WHEN @status::plant_status = 'discarded' THEN sqlc.narg(reason) END
WHERE organization_id = @organization_id AND barcode = @barcode AND status = 'returned'
RETURNING id;

-- name: ListReturns :many
-- Returns still needing something (@active): waiting for the plant, the refund, or a plant's check;
-- otherwise finished ones. Newest first.
SELECT r.id, r.name, r.shopify_status, r.requested_at, r.received_at, r.refunded,
       o.name AS order_name, o.shop, o.shopify_order_id
FROM returns r
JOIN orders o ON o.id = r.order_id
WHERE r.organization_id = @organization_id
  AND @active::boolean = (
      r.shopify_status IN ('REQUESTED', 'OPEN')
      OR (r.received_at IS NOT NULL AND NOT r.refunded AND r.shopify_status <> 'CANCELED')
      OR EXISTS (SELECT 1 FROM return_items ri JOIN plants p ON p.return_item_id = ri.id
                 WHERE ri.return_id = r.id AND p.status = 'returned'))
ORDER BY r.requested_at DESC, r.id DESC
LIMIT 200;

-- name: ListReturnItems :many
-- The returns' items: what's coming back and why, the order's sold plants that could be it, and the
-- plants received (still waiting for a check, or checked).
SELECT ri.id, ri.return_id, ri.quantity, ri.refunded_quantity, ri.reason, ri.customer_note,
       coalesce(oi.title, 'An item not in Plant Stock')::text AS title, ps.name AS pot_size,
       coalesce((SELECT array_agg(p.barcode ORDER BY p.barcode) FROM plants p
                 WHERE p.order_item_id = ri.order_item_id AND p.status = 'sold'), '{}')::text[] AS sold_barcodes,
       coalesce((SELECT array_agg(p.barcode ORDER BY p.barcode) FROM plants p
                 WHERE p.return_item_id = ri.id AND p.status = 'returned'), '{}')::text[] AS waiting_barcodes,
       (SELECT count(*) FROM plants p WHERE p.return_item_id = ri.id)::integer AS received_count
FROM return_items ri
LEFT JOIN order_items oi ON oi.id = ri.order_item_id
LEFT JOIN product_variants v ON v.id = oi.variant_id
LEFT JOIN pot_sizes ps ON ps.id = v.pot_size_id
WHERE ri.organization_id = @organization_id AND ri.return_id = ANY(@return_ids::bigint[])
ORDER BY ri.return_id, ri.id;

-- name: OrdersWithActiveReturns :many
-- Orders whose returns aren't settled yet: read again at each import, so a refund in Shopify is seen
-- even if it doesn't mark the order itself changed.
SELECT DISTINCT o.shopify_order_id
FROM returns r
JOIN orders o ON o.id = r.order_id
WHERE r.organization_id = @organization_id
  AND (r.shopify_status IN ('REQUESTED', 'OPEN') OR NOT r.refunded)
  AND r.shopify_status NOT IN ('CANCELED', 'DECLINED');

-- Refunds --

-- name: UpsertRefund :exec
-- Importing a refund again keeps its review.
INSERT INTO refunds (organization_id, order_id, shopify_refund_id, shopify_return_id, refunded_at, amount_cents, items)
VALUES (@organization_id, @order_id, @shopify_refund_id, @shopify_return_id, @refunded_at, @amount_cents, @items)
ON CONFLICT (organization_id, shopify_refund_id) DO UPDATE
SET shopify_return_id = excluded.shopify_return_id, amount_cents = excluded.amount_cents, items = excluded.items;

-- name: UnreviewedRefundsWithoutReturn :many
-- Refunds without a return that nobody has looked into yet, newest first.
SELECT f.id, f.refunded_at, f.amount_cents, f.items, o.name AS order_name, o.shop, o.shopify_order_id
FROM refunds f
JOIN orders o ON o.id = f.order_id
WHERE f.organization_id = @organization_id AND f.shopify_return_id IS NULL AND f.reviewed_at IS NULL
ORDER BY f.refunded_at DESC;

-- name: ReviewRefund :one
UPDATE refunds
SET reviewed_at = now(), reviewed_by_name = current_user_name()
WHERE organization_id = @organization_id AND id = @id AND shopify_return_id IS NULL AND reviewed_at IS NULL
RETURNING id;
