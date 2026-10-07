-- Inside one nursery (run through db.InTenant) --

-- name: ListPotSizes :many
SELECT * FROM pot_sizes
WHERE organization_id = @organization_id
ORDER BY sort_order, name;

-- name: CreatePotSize :one
-- Added at the end of the list.
INSERT INTO pot_sizes (organization_id, name, diameter_inches, height_inches, sellable, description, sort_order)
VALUES (@organization_id, @name, @diameter_inches, @height_inches, @sellable, @description,
        (SELECT coalesce(max(sort_order) + 1, 0) FROM pot_sizes WHERE organization_id = @organization_id))
RETURNING *;

-- name: UpdatePotSize :one
-- Changes only the fields that are given (NULL keeps the current value).
UPDATE pot_sizes
SET name            = coalesce(sqlc.narg(name), name),
    diameter_inches = coalesce(sqlc.narg(diameter_inches), diameter_inches),
    height_inches   = coalesce(sqlc.narg(height_inches), height_inches),
    sellable        = coalesce(sqlc.narg(sellable), sellable),
    description     = coalesce(sqlc.narg(description), description)
WHERE organization_id = @organization_id AND id = @id
RETURNING *;

-- name: SetPotSizeOrder :execrows
UPDATE pot_sizes
SET sort_order = @sort_order
WHERE organization_id = @organization_id AND id = @id;

-- name: DeletePotSize :execrows
DELETE FROM pot_sizes
WHERE organization_id = @organization_id AND id = @id;
-- name: GetPotSize :one
SELECT * FROM pot_sizes
WHERE organization_id = @organization_id AND id = @id;
