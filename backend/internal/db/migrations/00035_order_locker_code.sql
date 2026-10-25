-- Each order's locker code: 4 random digits staff set on the locker's PIN pad when they load the order.
-- Made by the database for every order (existing ones too, when the column is added), and never changed,
-- so the code staff set and the one the customer is sent later (by the notification emails, to come)
-- are always the same. Only staff who prepare orders see it.

-- +goose Up
ALTER TABLE orders ADD COLUMN locker_code text NOT NULL
    DEFAULT lpad(floor(random() * 10000)::int::text, 4, '0')
    CHECK (locker_code ~ '^[0-9]{4}$');

-- +goose Down
ALTER TABLE orders DROP COLUMN locker_code;
