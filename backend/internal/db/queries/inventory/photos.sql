-- Inside one nursery (run through db.InTenant) --

-- name: CreatePlantPhoto :one
INSERT INTO plant_photos (organization_id, plant_id, storage_key, taken_at, caption, uploaded_by)
VALUES (@organization_id, @plant_id, @storage_key, coalesce(sqlc.narg(taken_at)::timestamptz, now()), @caption, @uploaded_by)
RETURNING *;

-- name: ListPlantPhotos :many
-- Newest first, with who uploaded each.
SELECT ph.*, u.email AS uploaded_by_email
FROM plant_photos ph
LEFT JOIN users u ON u.id = ph.uploaded_by
WHERE ph.organization_id = @organization_id AND ph.plant_id = @plant_id
ORDER BY ph.taken_at DESC, ph.id DESC;

-- name: DeletePlantPhoto :one
DELETE FROM plant_photos
WHERE organization_id = @organization_id AND id = @id
RETURNING storage_key;
