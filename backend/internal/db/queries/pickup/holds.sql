-- Lockers, holds and bookings (inside one nursery, run through db.InTenant). "Live" means held (until
-- expires_at) or booked. Times come in as @now, not now(), so tests can set the clock.

-- name: ListPickupLockers :many
SELECT * FROM pickup_lockers WHERE organization_id = @organization_id ORDER BY id;

-- name: CreatePickupLocker :exec
-- A request at the same moment may have made it already: that's fine (and doesn't abort the transaction).
INSERT INTO pickup_lockers (organization_id, name) VALUES (@organization_id, @name)
ON CONFLICT (organization_id, name) DO NOTHING;

-- name: ExpirePickupHolds :exec
-- Before taking a slot: holds that have run out stop counting (the unique index only covers live rows).
UPDATE pickup_bookings SET status = 'expired'
WHERE organization_id = @organization_id AND status = 'held' AND expires_at <= @now;

-- name: CartPickupHold :one
-- The cart's hold that's still running (a cart holds at most one slot).
SELECT * FROM pickup_bookings
WHERE organization_id = @organization_id AND cart_token = @cart_token AND status = 'held' AND expires_at > @now
LIMIT 1;

-- name: PickupHoldByToken :one
SELECT * FROM pickup_bookings
WHERE organization_id = @organization_id AND hold_token = @hold_token AND cart_token = @cart_token;

-- name: CountVisitorPickupHolds :one
-- Running holds from this visitor's other carts.
SELECT count(*) FROM pickup_bookings
WHERE organization_id = @organization_id AND client_ip = @client_ip AND cart_token <> @cart_token
  AND status = 'held' AND expires_at > @now;

-- name: FreePickupLocker :one
-- A locker with nothing live in this slot.
SELECT l.id FROM pickup_lockers l
WHERE l.organization_id = @organization_id
  AND NOT EXISTS (
      SELECT 1 FROM pickup_bookings b
      WHERE b.organization_id = l.organization_id AND b.locker_id = l.id AND b.date = @date AND b.window_id = @window_id
        AND (b.status = 'booked' OR (b.status = 'held' AND b.expires_at > @now)))
ORDER BY l.id
LIMIT 1;

-- name: CreatePickupHold :one
INSERT INTO pickup_bookings (organization_id, locker_id, date, window_id, window_name, starts_at, ends_at,
                             hold_token, cart_token, client_ip, expires_at)
VALUES (@organization_id, @locker_id, @date, @window_id, @window_name, @starts_at, @ends_at,
        @hold_token, @cart_token, sqlc.narg(client_ip), @expires_at)
RETURNING *;

-- name: RenewPickupHold :one
-- Keeps a hold (or takes back an expired one: fails on the unique index if the slot was taken since).
UPDATE pickup_bookings SET status = 'held', expires_at = @expires_at
WHERE organization_id = @organization_id AND id = @id AND status IN ('held', 'expired')
RETURNING *;

-- name: ReleasePickupHold :exec
UPDATE pickup_bookings SET status = 'released'
WHERE organization_id = @organization_id AND id = @id AND status IN ('held', 'expired');

-- name: ListLivePickupBookings :many
-- Holds still running and bookings, from one date to another: what's taken.
SELECT * FROM pickup_bookings
WHERE organization_id = @organization_id AND date BETWEEN @from_date AND @to_date
  AND (status = 'booked' OR (status = 'held' AND expires_at > @now))
ORDER BY date, starts_at, id;

-- name: HasUpcomingPickupBookings :one
-- Before replacing the windows: any hold or booking for a window that hasn't ended.
SELECT EXISTS (
    SELECT 1 FROM pickup_bookings
    WHERE organization_id = @organization_id AND ends_at > @now
      AND (status = 'booked' OR (status = 'held' AND expires_at > @now)));
