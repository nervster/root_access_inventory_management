-- Inside one nursery (run through db.InTenant) --
--
-- A product is a plant name + variegation (a Shopify product); its variants are one per pot size
-- (Shopify variants), each with a SKU and price. A variant's stock is the number of for-sale plants
-- with the product's name and variegation in the variant's pot size.

-- name: CreateProduct :one
INSERT INTO products (organization_id, plant_name_id, variegation_id, title, description)
VALUES (@organization_id, @plant_name_id, @variegation_id, @title, @description)
RETURNING *;

-- name: ListProducts :many
SELECT pr.*, pn.full_name AS plant_name, v.name AS variegation
FROM products pr
JOIN plant_names pn ON pn.id = pr.plant_name_id
LEFT JOIN variegations v ON v.id = pr.variegation_id
WHERE pr.organization_id = @organization_id
ORDER BY pr.title;

-- name: GetProduct :one
SELECT pr.*, pn.full_name AS plant_name, v.name AS variegation
FROM products pr
JOIN plant_names pn ON pn.id = pr.plant_name_id
LEFT JOIN variegations v ON v.id = pr.variegation_id
WHERE pr.organization_id = @organization_id AND pr.id = @id;

-- name: UpdateProduct :one
UPDATE products
SET title       = coalesce(sqlc.narg(title), title),
    description = coalesce(sqlc.narg(description), description)
WHERE organization_id = @organization_id AND id = @id
RETURNING *;

-- name: ListVariantsWithStock :many
-- Every variant of the nursery's products (or of one product), smallest pot first, with its stock.
SELECT pv.*, ps.name AS pot_size, ps.sort_order AS pot_sort_order,
       (SELECT count(*) FROM plants p
        WHERE p.organization_id = pr.organization_id
          AND p.plant_name_id = pr.plant_name_id
          AND p.variegation_id IS NOT DISTINCT FROM pr.variegation_id
          AND p.pot_size_id = pv.pot_size_id
          AND p.status = 'for_sale') AS stock
FROM product_variants pv
JOIN products pr ON pr.id = pv.product_id
JOIN pot_sizes ps ON ps.id = pv.pot_size_id
WHERE pv.organization_id = @organization_id
  AND (sqlc.narg(product_id)::bigint IS NULL OR pv.product_id = sqlc.narg(product_id))
ORDER BY pv.product_id, ps.sort_order, ps.name;

-- name: UpsertVariant :one
-- Sets the SKU and price for one pot size of a product, creating the variant if needed.
INSERT INTO product_variants (organization_id, product_id, pot_size_id, sku, price_cents)
VALUES (@organization_id, @product_id, @pot_size_id, @sku, @price_cents)
ON CONFLICT (product_id, pot_size_id) DO UPDATE
SET sku = EXCLUDED.sku, price_cents = EXCLUDED.price_cents
RETURNING *;

-- name: DeleteVariant :execrows
DELETE FROM product_variants
WHERE organization_id = @organization_id AND product_id = @product_id AND pot_size_id = @pot_size_id;

-- name: SaleEligibility :many
-- For the chosen plants (a batch, or the listed barcodes): whether each can be marked for sale.
SELECT p.barcode, p.status, ps.sellable AS pot_sellable,
       EXISTS (SELECT 1 FROM products pr
               JOIN product_variants pv ON pv.product_id = pr.id
               WHERE pr.organization_id = p.organization_id
                 AND pr.plant_name_id = p.plant_name_id
                 AND pr.variegation_id IS NOT DISTINCT FROM p.variegation_id
                 AND pv.pot_size_id = p.pot_size_id) AS has_price
FROM plants p
JOIN pot_sizes ps ON ps.id = p.pot_size_id
WHERE p.organization_id = @organization_id
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR p.barcode = ANY(sqlc.narg(barcodes)::text[]))
ORDER BY p.barcode;

-- name: MarkForSale :many
-- Marks the chosen growing plants for sale when they're in a sellable pot and priced (a variant
-- exists for their name, variegation, and pot). Returns the marked barcodes.
UPDATE plants p
SET status = 'for_sale', updated_at = now()
FROM pot_sizes ps
WHERE p.organization_id = @organization_id
  AND p.status = 'growing'
  AND ps.id = p.pot_size_id AND ps.sellable
  AND EXISTS (SELECT 1 FROM products pr
              JOIN product_variants pv ON pv.product_id = pr.id
              WHERE pr.organization_id = p.organization_id
                AND pr.plant_name_id = p.plant_name_id
                AND pr.variegation_id IS NOT DISTINCT FROM p.variegation_id
                AND pv.pot_size_id = p.pot_size_id)
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR p.barcode = ANY(sqlc.narg(barcodes)::text[]))
RETURNING p.barcode;

-- name: UnmarkForSale :many
UPDATE plants
SET status = 'growing', updated_at = now()
WHERE organization_id = @organization_id AND status = 'for_sale' AND barcode = ANY(@barcodes::text[])
RETURNING barcode;
