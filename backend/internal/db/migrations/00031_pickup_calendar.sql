-- Pickup slots, step 1: each nursery's pickup windows and its pickup calendar (docs/ROADMAP.md, Pickup
-- decisions). Plant Stock, not the Shopify theme, decides which slots customers can book: a slot is one
-- locker × one date × one window. The windows are the same every day; the calendar is open by default,
-- and staff close exceptions, a whole day or a single window. Holds and bookings come in the next step.

-- +goose Up
-- The nursery's pickup rules. A nursery with no row uses the defaults.
CREATE TABLE pickup_settings (
    organization_id bigint  PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    lead_hours      integer NOT NULL DEFAULT 8 CHECK (lead_hours BETWEEN 0 AND 168),   -- a window can be booked only if it starts at least this far ahead
    booking_days    integer NOT NULL DEFAULT 30 CHECK (booking_days BETWEEN 1 AND 90) -- how far ahead customers can book
);

-- The day's windows, the same every day, in the nursery's time zone. A window can run past midnight
-- (e.g. 8 PM for 12 hours, "Overnight"); it belongs to the date it starts on.
CREATE TABLE pickup_windows (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text        NOT NULL CHECK (name <> ''),
    start_minute    integer     NOT NULL CHECK (start_minute BETWEEN 0 AND 1439), -- minutes after midnight
    length_minutes  integer     NOT NULL CHECK (length_minutes BETWEEN 1 AND 1440),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    UNIQUE (organization_id, start_minute),
    UNIQUE (organization_id, id)
);

-- Exceptions to "open": a whole date (window_id NULL) or one window on a date.
CREATE TABLE pickup_closures (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    date            date        NOT NULL,
    window_id       bigint,
    reason          text,
    closed_by_name  text, -- who closed it, by name (email if none), as it was then
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (organization_id, date, window_id),
    FOREIGN KEY (organization_id, window_id) REFERENCES pickup_windows (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX pickup_closures_date_idx ON pickup_closures (organization_id, date);

GRANT SELECT, INSERT, UPDATE, DELETE ON pickup_settings, pickup_windows, pickup_closures TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE pickup_settings ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pickup_settings
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE pickup_windows ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pickup_windows
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE pickup_closures ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pickup_closures
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE pickup_closures;
DROP TABLE pickup_windows;
DROP TABLE pickup_settings;
