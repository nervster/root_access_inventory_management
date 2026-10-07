-- Plant names and variegations: the shared list plus the nursery's own (Row-Level Security
-- decides which rows are visible, so these run inside db.InTenant).

-- name: SearchPlantNames :many
-- Every part of the full name matches, e.g. "dragon" finds Alocasia baginda 'Dragon Scale'.
-- Genera and finer ranks only (not families), shortest names first.
SELECT * FROM plant_names
WHERE rank <> 'family' AND full_name ILIKE '%' || @term::text || '%'
ORDER BY length(full_name), full_name
LIMIT 50;

-- name: GetPlantName :one
SELECT * FROM plant_names
WHERE id = @id;

-- name: ListVariegations :many
SELECT * FROM variegations
ORDER BY name;

-- name: GetVariegation :one
SELECT * FROM variegations
WHERE id = @id;
