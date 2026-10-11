-- Plant names and variegations: the shared list plus the nursery's own (Row-Level Security
-- decides which rows are visible, so these run inside db.InTenant).

-- name: SearchPlantNames :many
-- Every part of the full name matches, e.g. "dragon" finds Alocasia baginda 'Dragon Scale'.
-- Families only when @include_families (when choosing where a new genus goes); shortest names first.
SELECT * FROM plant_names
WHERE (rank <> 'family' OR @include_families::boolean) AND full_name ILIKE '%' || @term::text || '%'
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

-- name: PlantNameUnder :one
-- A name already under @parent_id (shared or this nursery's own), to refuse duplicates.
SELECT * FROM plant_names
WHERE parent_id IS NOT DISTINCT FROM sqlc.narg(parent_id)::bigint AND lower(name) = lower(@name)
LIMIT 1;

-- name: CreatePlantName :one
INSERT INTO plant_names (organization_id, parent_id, rank, name, full_name, notes)
VALUES (@organization_id, sqlc.narg(parent_id), @rank, @name, @full_name, @notes)
RETURNING *;

-- name: DeletePlantName :execrows
-- Only the nursery's own names (Row-Level Security hides the shared list from changes).
DELETE FROM plant_names
WHERE id = @id;

-- name: VariegationNamed :one
SELECT * FROM variegations
WHERE lower(name) = lower(@name)
LIMIT 1;

-- name: CreateVariegation :one
INSERT INTO variegations (organization_id, name, notes)
VALUES (@organization_id, @name, @notes)
RETURNING *;

-- name: DeleteVariegation :execrows
DELETE FROM variegations
WHERE id = @id;
