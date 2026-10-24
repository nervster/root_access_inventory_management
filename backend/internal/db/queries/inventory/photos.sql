-- Inside one nursery (run through db.InTenant) --

-- name: CreatePlantPhoto :one
-- uploaded_by_name: the uploader's name as it is now (see migration 00014).
INSERT INTO plant_photos (organization_id, plant_id, storage_key, taken_at, caption, uploaded_by, uploaded_by_name)
VALUES (@organization_id, @plant_id, @storage_key, coalesce(sqlc.narg(taken_at)::timestamptz, now()), @caption, @uploaded_by,
        current_user_name())
RETURNING *;

-- name: ListPlantPhotos :many
-- Newest first.
SELECT * FROM plant_photos
WHERE organization_id = @organization_id AND plant_id = @plant_id
ORDER BY taken_at DESC, id DESC;

-- name: SetPlantPhotoCaption :one
-- NULL clears it. For captions added after the photo, e.g. during a photo session.
UPDATE plant_photos SET caption = sqlc.narg(caption)
WHERE organization_id = @organization_id AND id = @id
RETURNING *;

-- name: DeletePlantPhoto :one
DELETE FROM plant_photos
WHERE organization_id = @organization_id AND id = @id
RETURNING storage_key;

-- name: ListProductPhotos :many
-- The nursery's product images (or one product's), each product's in order.
SELECT pp.product_id, pp.photo_id, pp.position, ph.storage_key
FROM product_photos pp
JOIN plant_photos ph ON ph.id = pp.photo_id
WHERE pp.organization_id = @organization_id
  AND (sqlc.narg(product_id)::bigint IS NULL OR pp.product_id = sqlc.narg(product_id))
ORDER BY pp.product_id, pp.position;

-- name: ProductPhotoCandidates :many
-- Photos that can be a product's images: of plants with the product's name and variegation, newest
-- first, and whether each is already chosen.
SELECT ph.id, ph.storage_key, ph.taken_at, ph.caption, p.barcode,
       EXISTS (SELECT 1 FROM product_photos pp WHERE pp.product_id = pr.id AND pp.photo_id = ph.id) AS chosen
FROM products pr
JOIN plants p ON p.organization_id = pr.organization_id
             AND p.plant_name_id = pr.plant_name_id
             AND p.variegation_id IS NOT DISTINCT FROM pr.variegation_id
JOIN plant_photos ph ON ph.plant_id = p.id
WHERE pr.organization_id = @organization_id AND pr.id = @product_id
ORDER BY ph.taken_at DESC, ph.id DESC
LIMIT 200;

-- name: CountEligibleProductPhotos :one
-- How many of @photo_ids are photos of plants matching the product (to refuse any that aren't).
SELECT count(*)
FROM products pr
JOIN plants p ON p.organization_id = pr.organization_id
             AND p.plant_name_id = pr.plant_name_id
             AND p.variegation_id IS NOT DISTINCT FROM pr.variegation_id
JOIN plant_photos ph ON ph.plant_id = p.id
WHERE pr.organization_id = @organization_id AND pr.id = @product_id AND ph.id = ANY(@photo_ids::bigint[]);

-- name: ClearProductPhotos :exec
DELETE FROM product_photos
WHERE organization_id = @organization_id AND product_id = @product_id;

-- name: AddProductPhoto :exec
INSERT INTO product_photos (organization_id, product_id, photo_id, position)
VALUES (@organization_id, @product_id, @photo_id, @position);
