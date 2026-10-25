-- Pickup slots, step 4: each order's pickup, from the cart attributes the theme saves
-- (docs/pickup-storefront.md): the date and window it booked, the shopper's optional note, and why its
-- booking needs staff (a clash, or no valid slot). The booking itself is in pickup_bookings (order_id).

-- +goose Up
ALTER TABLE orders
    ADD COLUMN pickup_date    date,
    ADD COLUMN pickup_window  text,
    ADD COLUMN pickup_note    text, -- written by the customer: cleared by Shopify's privacy requests
    ADD COLUMN pickup_problem text; -- e.g. "Another order already has Oct 24, Morning"; NULL when fine

-- +goose Down
ALTER TABLE orders
    DROP COLUMN pickup_problem,
    DROP COLUMN pickup_note,
    DROP COLUMN pickup_window,
    DROP COLUMN pickup_date;
