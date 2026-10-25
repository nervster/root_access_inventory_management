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

-- name: ListPlantEvents :many
-- A plant's history (written by triggers, see migrations 00011 to 00013), oldest first. The name of
-- who made each change is copied into the event, so it shows even after they leave the team.
SELECT e.id, e.type, e.details, e.occurred_at, e.changed_by_name
FROM plant_events e
WHERE e.organization_id = @organization_id AND e.plant_id = @plant_id
ORDER BY e.occurred_at, e.id;

-- name: GetPlantStatuses :many
-- The barcode and status of each listed plant that exists (to report unknown or finished ones).
SELECT barcode, status FROM plants
WHERE organization_id = @organization_id AND barcode = ANY(@barcodes::text[]);

-- name: MovePlantsToPot :many
-- Moves growing and for-sale plants (a whole batch, or the listed barcodes) into @to_pot_size_id;
-- plants already in that pot aren't touched. A for-sale plant moved into a pot that can't be sold
-- goes back to growing. The history trigger records each move. Returns the moved plants' IDs.
UPDATE plants p
SET pot_size_id = @to_pot_size_id,
    status      = CASE WHEN p.status = 'for_sale' AND NOT @to_pot_sellable::boolean THEN 'growing'::plant_status ELSE p.status END,
    updated_at  = now()
WHERE p.organization_id = @organization_id
  AND p.status IN ('growing', 'for_sale')
  AND p.pot_size_id <> @to_pot_size_id
  AND (sqlc.narg(batch_id)::bigint IS NULL OR p.batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR p.barcode = ANY(sqlc.narg(barcodes)::text[]))
RETURNING p.id;

-- name: DiscardPlants :many
UPDATE plants
SET status = 'discarded', discarded_at = now(), discard_reason = @reason, updated_at = now()
WHERE organization_id = @organization_id AND barcode = ANY(@barcodes::text[]) AND status IN ('growing', 'for_sale')
RETURNING barcode;

-- name: CorrectPlants :many
-- Fixes the name and/or variegation of growing and for-sale plants (a batch, or the listed
-- barcodes). A NULL @plant_name_id keeps the name; the variegation changes only when
-- @set_variegation (to @variegation_id, which may be NULL for none). Plants already right aren't
-- touched; the history trigger records each correction. Returns the corrected plants' IDs.
UPDATE plants
SET plant_name_id  = coalesce(sqlc.narg(plant_name_id)::bigint, plant_name_id),
    variegation_id = CASE WHEN @set_variegation::boolean THEN sqlc.narg(variegation_id)::bigint ELSE variegation_id END,
    updated_at     = now()
WHERE organization_id = @organization_id
  AND status IN ('growing', 'for_sale')
  AND (sqlc.narg(batch_id)::bigint IS NULL OR batch_id = sqlc.narg(batch_id))
  AND (sqlc.narg(barcodes)::text[] IS NULL OR barcode = ANY(sqlc.narg(barcodes)::text[]))
  AND (plant_name_id IS DISTINCT FROM coalesce(sqlc.narg(plant_name_id)::bigint, plant_name_id)
       OR (@set_variegation AND variegation_id IS DISTINCT FROM sqlc.narg(variegation_id)::bigint))
RETURNING id;

-- name: UpdatePlantNotes :one
-- An empty note clears it.
UPDATE plants
SET notes = nullif(@notes::text, ''), updated_at = now()
WHERE organization_id = @organization_id AND barcode = @barcode
RETURNING id;
