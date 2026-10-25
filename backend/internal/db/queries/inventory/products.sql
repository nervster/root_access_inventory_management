-- Inside one nursery (run through db.InTenant) --
--
-- A product is a plant name + variegation (a Shopify product); its variants are one per pot size
-- (Shopify variants), each with a SKU and a price (NULL until someone sets it). Both are created
-- automatically the first time plants of that kind are marked for sale in that pot. A variant's
-- stock is the number of for-sale plants with the product's name and variegation in its pot size.

-- name: EnsureProduct :one
-- The product for a plant name + variegation, created with @title if it doesn't exist yet.
INSERT INTO products (organization_id, plant_name_id, variegation_id, title)
VALUES (@organization_id, @plant_name_id, @variegation_id, @title)
ON CONFLICT (organization_id, plant_name_id, variegation_id) DO UPDATE SET title = products.title
RETURNING id;

-- name: ListProducts :many
-- Products that have been for sale (they have at least one variant), by title.
SELECT pr.*, pn.full_name AS plant_name, v.name AS variegation
FROM products pr
JOIN plant_names pn ON pn.id = pr.plant_name_id
LEFT JOIN variegations v ON v.id = pr.variegation_id
WHERE pr.organization_id = @organization_id
  AND EXISTS (SELECT 1 FROM product_variants pv WHERE pv.product_id = pr.id)
ORDER BY pr.title;

-- name: GetProduct :one
SELECT pr.*, pn.full_name AS plant_name, v.name AS variegation
FROM products pr
JOIN plant_names pn ON pn.id = pr.plant_name_id
LEFT JOIN variegations v ON v.id = pr.variegation_id
WHERE pr.organization_id = @organization_id AND pr.id = @id;

-- name: UpdateProduct :one
-- Changes only the fields that are given (NULL keeps the current value; an empty list clears the tags).
UPDATE products
SET title        = coalesce(sqlc.narg(title), title),
    description  = coalesce(sqlc.narg(description), description),
    product_type = coalesce(sqlc.narg(product_type), product_type),
    tags         = coalesce(sqlc.narg(tags)::text[], tags)
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

-- name: MissingVariants :many
-- The plant name + variegation + pot combinations among the chosen plants (a batch, or the listed
-- barcodes, with one of @statuses) in sellable pots that have no variant yet: the SKUs to create
-- when they go on sale, or when for-sale plants move into a pot they weren't sold in before.
SELECT DISTINCT p.plant_name_id, pn.full_name AS plant_name,
                p.variegation_id, v.name AS variegation,
                p.pot_size_id, ps.name AS pot_size
FROM plants p
JOIN pot_sizes ps ON ps.id = p.pot_size_id AND ps.sellable
JOIN plant_names pn ON pn.id = p.plant_name_id
LEFT JOIN variegations v ON v.id = p.variegation_id
WHERE p.organization_id = @organization_id
  AND p.status::text = ANY(@statuses::text[]) -- text: pgx can't send a list of a custom enum
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR p.barcode = ANY(sqlc.narg(barcodes)::text[]))
  AND NOT EXISTS (SELECT 1 FROM products pr
                  JOIN product_variants pv ON pv.product_id = pr.id
                  WHERE pr.organization_id = p.organization_id
                    AND pr.plant_name_id = p.plant_name_id
                    AND pr.variegation_id IS NOT DISTINCT FROM p.variegation_id
                    AND pv.pot_size_id = p.pot_size_id)
ORDER BY pn.full_name, v.name, ps.name;

-- name: SKUTaken :one
SELECT EXISTS (SELECT 1 FROM product_variants WHERE organization_id = @organization_id AND sku = @sku);

-- name: CreateVariant :one
INSERT INTO product_variants (organization_id, product_id, pot_size_id, sku)
VALUES (@organization_id, @product_id, @pot_size_id, @sku)
RETURNING *;

-- name: UpdateVariant :one
-- Changes the SKU and/or price of an existing variant (NULL keeps a value).
UPDATE product_variants
SET sku         = coalesce(sqlc.narg(sku), sku),
    price_cents = coalesce(sqlc.narg(price_cents), price_cents)
WHERE organization_id = @organization_id AND product_id = @product_id AND pot_size_id = @pot_size_id
RETURNING *;

-- name: DeleteVariant :execrows
DELETE FROM product_variants
WHERE organization_id = @organization_id AND product_id = @product_id AND pot_size_id = @pot_size_id;

-- name: SaleEligibility :many
-- For the chosen plants (a batch, or the listed barcodes): their status and whether their pot can be sold.
SELECT p.barcode, p.status, ps.sellable AS pot_sellable
FROM plants p
JOIN pot_sizes ps ON ps.id = p.pot_size_id
WHERE p.organization_id = @organization_id
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR p.barcode = ANY(sqlc.narg(barcodes)::text[]))
ORDER BY p.barcode;

-- name: MarkForSale :many
-- Marks the chosen growing plants in sellable pots for sale. Run after their variants exist
-- (MissingVariants + CreateVariant); a plant without one is left alone. Returns the marked barcodes.
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

-- name: ListProductEvents :many
-- A product's price and details history (written by triggers, see migration 00029), oldest first.
-- The name of who made each change is copied into the event, so it shows even after they leave.
SELECT e.id, e.type, e.details, e.occurred_at, e.changed_by_name
FROM product_events e
WHERE e.organization_id = @organization_id AND e.product_id = @product_id
ORDER BY e.occurred_at, e.id;
