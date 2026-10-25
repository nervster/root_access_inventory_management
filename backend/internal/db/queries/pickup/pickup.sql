-- Pickup windows and calendar (inside one nursery, run through db.InTenant) --

-- name: GetPickupSettings :one
-- No row: the nursery uses the defaults (see migration 00031).
SELECT lead_hours, booking_days FROM pickup_settings WHERE organization_id = @organization_id;

-- name: SetPickupSettings :one
INSERT INTO pickup_settings (organization_id, lead_hours, booking_days)
VALUES (@organization_id, @lead_hours, @booking_days)
ON CONFLICT (organization_id) DO UPDATE SET lead_hours = excluded.lead_hours, booking_days = excluded.booking_days
RETURNING lead_hours, booking_days;

-- name: ListPickupWindows :many
SELECT * FROM pickup_windows WHERE organization_id = @organization_id ORDER BY start_minute;

-- name: DeletePickupWindows :exec
-- Before setting up new windows. Their closures go too (ON DELETE CASCADE).
DELETE FROM pickup_windows WHERE organization_id = @organization_id;

-- name: CreatePickupWindow :one
INSERT INTO pickup_windows (organization_id, name, start_minute, length_minutes)
VALUES (@organization_id, @name, @start_minute, @length_minutes)
RETURNING *;

-- name: RenamePickupWindow :one
UPDATE pickup_windows SET name = @name
WHERE organization_id = @organization_id AND id = @id
RETURNING *;

-- name: ListPickupClosures :many
SELECT * FROM pickup_closures
WHERE organization_id = @organization_id AND date BETWEEN @from_date AND @to_date
ORDER BY date, window_id NULLS FIRST;

-- name: ClosePickup :one
-- Closing what's already closed just updates the reason. closed_by_name: see migration 00014.
INSERT INTO pickup_closures (organization_id, date, window_id, reason, closed_by_name)
VALUES (@organization_id, @date, sqlc.narg(window_id), sqlc.narg(reason), current_user_name())
ON CONFLICT (organization_id, date, window_id) DO UPDATE SET reason = excluded.reason
RETURNING *;

-- name: ReopenPickup :execrows
DELETE FROM pickup_closures
WHERE organization_id = @organization_id AND date = @date AND window_id IS NOT DISTINCT FROM sqlc.narg(window_id);
