-- Inside one nursery (run through db.InTenant) --

-- name: ReserveInventoryNumbers :one
-- Reserves one batch number and @plant_count plant numbers. The first call creates the nursery's
-- counter with @barcode_prefix.
INSERT INTO inventory_counters (organization_id, barcode_prefix, next_plant_number, next_batch_number)
VALUES (@organization_id, @barcode_prefix, 1 + @plant_count::bigint, 2)
ON CONFLICT (organization_id) DO UPDATE
SET next_plant_number = inventory_counters.next_plant_number + @plant_count::bigint,
    next_batch_number = inventory_counters.next_batch_number + 1
RETURNING barcode_prefix,
          (next_plant_number - @plant_count::bigint)::bigint AS first_plant_number,
          (next_batch_number - 1)::bigint AS batch_number;

-- name: CreateBatch :one
INSERT INTO batches (organization_id, code, plant_name_id, variegation_id, source, received_on, unit_cost_cents, notes, created_by)
VALUES (@organization_id, @code, @plant_name_id, @variegation_id, @source, @received_on, @unit_cost_cents, @notes, @created_by)
RETURNING *;

-- name: CreateBatchPlants :many
-- One plant per number from @first_number to @last_number, all in the batch's first pot.
INSERT INTO plants (organization_id, barcode, batch_id, plant_name_id, variegation_id, pot_size_id)
SELECT @organization_id, @barcode_prefix::text || '-' || lpad(n::text, 6, '0'), @batch_id, @plant_name_id, @variegation_id, @pot_size_id
FROM generate_series(@first_number::bigint, @last_number::bigint) AS n
ORDER BY n
RETURNING *;

-- name: ListBatches :many
-- Newest first, with how many of the batch's plants are in each status.
SELECT b.*,
       pn.full_name AS plant_name,
       v.name AS variegation,
       count(p.id) FILTER (WHERE p.status = 'growing')   AS growing_count,
       count(p.id) FILTER (WHERE p.status = 'for_sale')  AS for_sale_count,
       count(p.id) FILTER (WHERE p.status = 'sold')      AS sold_count,
       count(p.id) FILTER (WHERE p.status = 'discarded') AS discarded_count
FROM batches b
JOIN plant_names pn ON pn.id = b.plant_name_id
LEFT JOIN variegations v ON v.id = b.variegation_id
LEFT JOIN plants p ON p.batch_id = b.id
WHERE b.organization_id = @organization_id
GROUP BY b.id, pn.full_name, v.name
ORDER BY b.received_on DESC, b.id DESC;

-- name: GetBatch :one
SELECT b.*,
       pn.full_name AS plant_name,
       v.name AS variegation,
       count(p.id) FILTER (WHERE p.status = 'growing')   AS growing_count,
       count(p.id) FILTER (WHERE p.status = 'for_sale')  AS for_sale_count,
       count(p.id) FILTER (WHERE p.status = 'sold')      AS sold_count,
       count(p.id) FILTER (WHERE p.status = 'discarded') AS discarded_count
FROM batches b
JOIN plant_names pn ON pn.id = b.plant_name_id
LEFT JOIN variegations v ON v.id = b.variegation_id
LEFT JOIN plants p ON p.batch_id = b.id
WHERE b.organization_id = @organization_id AND b.id = @id
GROUP BY b.id, pn.full_name, v.name;
