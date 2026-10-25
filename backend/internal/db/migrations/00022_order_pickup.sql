-- Handing orders over: staff mark an order ready for pickup (in the locker), then picked up. Who and
-- when are recorded, names copied in like plant history (00013).

-- +goose Up
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN ('open', 'ready_for_pickup', 'picked_up', 'cancelled'));
ALTER TABLE orders
    ADD COLUMN ready_at          timestamptz,
    ADD COLUMN ready_by_name     text,
    ADD COLUMN picked_up_at      timestamptz,
    ADD COLUMN picked_up_by_name text;

-- +goose Down
UPDATE orders SET status = 'open' WHERE status = 'ready_for_pickup';
ALTER TABLE orders DROP COLUMN picked_up_by_name, DROP COLUMN picked_up_at, DROP COLUMN ready_by_name, DROP COLUMN ready_at;
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN ('open', 'picked_up', 'cancelled'));
