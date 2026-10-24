-- Pickup slots, step 2: lockers, and the holds and bookings that take a slot (docs/pickup-storefront.md).
-- A slot is one locker × one date × one window. A cart holds a slot for 30 minutes while the shopper pays;
-- when Plant Stock imports the order, the hold becomes a booking. At most one live hold or booking per slot,
-- enforced here, so two shoppers asking at the same moment can't both win.

-- +goose Up
CREATE TABLE pickup_lockers (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text        NOT NULL CHECK (name <> ''),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    UNIQUE (organization_id, id)
);

CREATE TYPE pickup_booking_status AS ENUM (
    'held',      -- a cart holds it until expires_at
    'booked',    -- an order has it
    'expired',   -- the hold ran out (the cart can take it again if it's still free)
    'released',  -- the cart gave it back, or chose another slot
    'cancelled'  -- the order was cancelled
);

CREATE TABLE pickup_bookings (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint                NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    locker_id       bigint                NOT NULL,
    date            date                  NOT NULL,
    window_id       bigint,               -- NULL once its window is replaced; the copies below remain
    window_name     text                  NOT NULL, -- as it was when held
    starts_at       timestamptz           NOT NULL,
    ends_at         timestamptz           NOT NULL,
    status          pickup_booking_status NOT NULL DEFAULT 'held',
    hold_token      text                  NOT NULL UNIQUE, -- the holdId the cart keeps (random, so it can't be guessed)
    cart_token      text                  NOT NULL,
    client_ip       text,                 -- for the limit on holds per visitor
    expires_at      timestamptz           NOT NULL, -- when a hold runs out (kept on bookings, unused)
    order_id        bigint,               -- set when it becomes a booking
    created_at      timestamptz           NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, locker_id) REFERENCES pickup_lockers (organization_id, id),
    FOREIGN KEY (organization_id, window_id) REFERENCES pickup_windows (organization_id, id) ON DELETE SET NULL (window_id),
    FOREIGN KEY (organization_id, order_id) REFERENCES orders (organization_id, id) ON DELETE SET NULL (order_id)
);
-- The rule: one live hold or booking per locker slot. (An expired hold isn't live: it's marked expired before
-- the slot is taken again.)
CREATE UNIQUE INDEX pickup_bookings_one_per_slot ON pickup_bookings (organization_id, locker_id, date, window_id)
    WHERE status IN ('held', 'booked');
CREATE INDEX pickup_bookings_cart_idx ON pickup_bookings (organization_id, cart_token) WHERE status = 'held';
CREATE INDEX pickup_bookings_date_idx ON pickup_bookings (organization_id, date);

GRANT SELECT, INSERT, UPDATE, DELETE ON pickup_lockers, pickup_bookings TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE pickup_lockers ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pickup_lockers
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE pickup_bookings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pickup_bookings
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE pickup_bookings;
DROP TYPE pickup_booking_status;
DROP TABLE pickup_lockers;
