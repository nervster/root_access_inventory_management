-- A plant can come back from a customer (a return): it waits for a check before going back on sale,
-- back to growing, or out. Its own migration: Postgres can't use a new enum value in the transaction
-- that adds it.

-- +goose Up
ALTER TYPE plant_status ADD VALUE IF NOT EXISTS 'returned' AFTER 'sold'; -- IF NOT EXISTS: Down can't remove it
ALTER TYPE plant_event_type ADD VALUE IF NOT EXISTS 'returned';

-- +goose Down
-- Postgres can't remove values from an enum; 00024's Down already settled returned plants.
SELECT 1;
