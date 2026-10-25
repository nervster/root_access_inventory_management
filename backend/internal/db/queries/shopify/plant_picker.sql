-- Choosing plants from the storefront (docs/plant-picker-storefront.md), inside one nursery, run through
-- db.InTenant. A plant can be chosen when it's for sale and has a photo. "Live" holds are held and not yet run
-- out. Times come in as @now, not now(), so tests can set the clock.

-- name: PickerVariant :one
-- The SKU a store's variant is (the theme knows the variant by its number).
SELECT sv.variant_id FROM shopify_variants sv
WHERE sv.organization_id = @organization_id AND sv.shop = @shop AND sv.shopify_variant_id = @shopify_variant_id;

-- name: PickablePlants :many
-- The SKU's plants a cart can choose, oldest first, each with its latest photo: for sale, with a photo, and
-- not held by another cart. held: this cart holds it.
SELECT p.id, photo.storage_key,
       EXISTS (SELECT 1 FROM plant_holds h
               WHERE h.organization_id = p.organization_id AND h.plant_id = p.id AND h.cart_token = @cart_token
                 AND h.status = 'held' AND h.expires_at > @now) AS held
FROM product_variants v
JOIN products pr ON pr.id = v.product_id
JOIN plants p ON p.organization_id = v.organization_id
             AND p.plant_name_id = pr.plant_name_id
             AND p.variegation_id IS NOT DISTINCT FROM pr.variegation_id
             AND p.pot_size_id = v.pot_size_id
CROSS JOIN LATERAL (
    SELECT ph.storage_key FROM plant_photos ph
    WHERE ph.organization_id = p.organization_id AND ph.plant_id = p.id
    ORDER BY ph.taken_at DESC, ph.id DESC
    LIMIT 1
) photo
WHERE v.organization_id = @organization_id AND v.id = @variant_id
  AND p.status = 'for_sale'
  AND NOT EXISTS (SELECT 1 FROM plant_holds h
                  WHERE h.organization_id = p.organization_id AND h.plant_id = p.id AND h.cart_token <> @cart_token
                    AND h.status = 'held' AND h.expires_at > @now)
ORDER BY p.created_at, p.id;

-- name: ExpirePlantHolds :exec
-- Before holding plants: holds that have run out stop counting (the unique index only covers held rows).
UPDATE plant_holds SET status = 'expired'
WHERE organization_id = @organization_id AND status = 'held' AND expires_at <= @now;

-- name: LockChoosablePlants :many
-- Of these plants, the ones that can be chosen at all (for sale, with a photo), locked until the transaction
-- ends: a second cart holding the same plant waits here, then sees the first one's hold.
SELECT p.id FROM plants p
WHERE p.organization_id = @organization_id AND p.id = ANY(@plant_ids::bigint[])
  AND p.status = 'for_sale'
  AND EXISTS (SELECT 1 FROM plant_photos ph WHERE ph.organization_id = p.organization_id AND ph.plant_id = p.id)
ORDER BY p.id
FOR UPDATE OF p;

-- name: PlantsHeldByOtherCarts :many
SELECT plant_id FROM plant_holds
WHERE organization_id = @organization_id AND plant_id = ANY(@plant_ids::bigint[]) AND cart_token <> @cart_token
  AND status = 'held' AND expires_at > @now;

-- name: CountVisitorPlantHolds :one
-- Live holds from this visitor (any of their carts), apart from these plants in this cart, which are being
-- held again.
SELECT count(*) FROM plant_holds
WHERE organization_id = @organization_id AND client_ip = @client_ip AND status = 'held' AND expires_at > @now
  AND NOT (cart_token = @cart_token AND plant_id = ANY(@plant_ids::bigint[]));

-- name: RenewCartPlantHolds :many
-- Keeps the cart's holds on these plants (or takes back ones that ran out), returning the plants renewed.
-- A cart has at most one held or expired hold per plant: releasing ends it, and holding again makes a new one.
UPDATE plant_holds SET status = 'held', expires_at = @expires_at
WHERE organization_id = @organization_id AND cart_token = @cart_token AND plant_id = ANY(@plant_ids::bigint[])
  AND status IN ('held', 'expired')
RETURNING plant_id;

-- name: CreatePlantHolds :exec
INSERT INTO plant_holds (organization_id, plant_id, cart_token, client_ip, expires_at)
SELECT @organization_id, unnest(@plant_ids::bigint[]), @cart_token, sqlc.narg(client_ip), @expires_at;

-- name: ReleaseCartPlantHolds :exec
UPDATE plant_holds SET status = 'released'
WHERE organization_id = @organization_id AND cart_token = @cart_token AND plant_id = ANY(@plant_ids::bigint[])
  AND status IN ('held', 'expired');
