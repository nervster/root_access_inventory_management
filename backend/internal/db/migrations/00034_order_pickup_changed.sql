-- Customers can change their pickup time on the store's My pickup page (docs/pickup-storefront.md), even
-- after staff mark the order ready: when they last did, so staff can see an order in the locker now has
-- another time.

-- +goose Up
ALTER TABLE orders ADD COLUMN pickup_changed_at timestamptz;

-- +goose Down
ALTER TABLE orders DROP COLUMN pickup_changed_at;
