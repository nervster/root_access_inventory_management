-- Inside one nursery (run through db.InTenant) --

-- name: ListPlants :many
-- Filters are optional (NULL = any). Ordered by barcode.
SELECT p.*,
       b.code AS batch_code,
       pn.full_name AS plant_name,
       v.name AS variegation,
       ps.name AS pot_size
FROM plants p
JOIN batches b ON b.id = p.batch_id
JOIN plant_names pn ON pn.id = p.plant_name_id
LEFT JOIN variegations v ON v.id = p.variegation_id
JOIN pot_sizes ps ON ps.id = p.pot_size_id
WHERE p.organization_id = @organization_id
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(status)::plant_status IS NULL OR p.status = sqlc.narg(status))
  AND (sqlc.narg(pot_size_id)::bigint IS NULL OR p.pot_size_id = sqlc.narg(pot_size_id))
ORDER BY p.barcode
LIMIT 1000;

-- name: GetPlantByBarcode :one
SELECT p.*,
       b.code AS batch_code,
       pn.full_name AS plant_name,
       v.name AS variegation,
       ps.name AS pot_size
FROM plants p
JOIN batches b ON b.id = p.batch_id
JOIN plant_names pn ON pn.id = p.plant_name_id
LEFT JOIN variegations v ON v.id = p.variegation_id
JOIN pot_sizes ps ON ps.id = p.pot_size_id
WHERE p.organization_id = @organization_id AND p.barcode = @barcode;

-- name: ListPotChanges :many
-- A plant's pot history, oldest first.
SELECT pc.id, pc.changed_at, pc.from_pot_size_id, pc.to_pot_size_id,
       from_ps.name AS from_pot_size,
       to_ps.name AS to_pot_size,
       u.email AS changed_by_email
FROM pot_changes pc
LEFT JOIN pot_sizes from_ps ON from_ps.id = pc.from_pot_size_id
JOIN pot_sizes to_ps ON to_ps.id = pc.to_pot_size_id
LEFT JOIN users u ON u.id = pc.changed_by
WHERE pc.organization_id = @organization_id AND pc.plant_id = @plant_id
ORDER BY pc.changed_at, pc.id;

-- name: GetPlantStatuses :many
-- The barcode and status of each listed plant that exists (to report unknown or finished ones).
SELECT barcode, status FROM plants
WHERE organization_id = @organization_id AND barcode = ANY(@barcodes::text[]);

-- name: MovePlantsToPot :many
-- Moves growing and for-sale plants (a whole batch, or the listed barcodes) into @to_pot_size_id and
-- records each move in their pot history. Plants already in that pot aren't touched. A for-sale
-- plant moved into a pot that can't be sold goes back to growing. Returns the moved plants' IDs.
WITH chosen AS (
    SELECT id, pot_size_id AS from_pot_size_id
    FROM plants
    WHERE organization_id = @organization_id
      AND status IN ('growing', 'for_sale')
      AND pot_size_id <> @to_pot_size_id
      AND (sqlc.narg(batch_id)::bigint IS NULL OR batch_id = sqlc.narg(batch_id))
      AND (sqlc.narg(barcodes)::text[] IS NULL OR barcode = ANY(sqlc.narg(barcodes)::text[]))
    FOR UPDATE
), moved AS (
    UPDATE plants p
    SET pot_size_id = @to_pot_size_id,
        status      = CASE WHEN p.status = 'for_sale' AND NOT @to_pot_sellable::boolean THEN 'growing'::plant_status ELSE p.status END,
        updated_at  = now()
    FROM chosen
    WHERE p.id = chosen.id
    RETURNING p.id, chosen.from_pot_size_id
)
INSERT INTO pot_changes (organization_id, plant_id, from_pot_size_id, to_pot_size_id, changed_by)
SELECT @organization_id, moved.id, moved.from_pot_size_id, @to_pot_size_id, @changed_by
FROM moved
RETURNING plant_id;

-- name: DiscardPlants :many
UPDATE plants
SET status = 'discarded', discarded_at = now(), discard_reason = @reason, updated_at = now()
WHERE organization_id = @organization_id AND barcode = ANY(@barcodes::text[]) AND status IN ('growing', 'for_sale')
RETURNING barcode;
