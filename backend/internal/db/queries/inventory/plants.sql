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
