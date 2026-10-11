-- A plant can be held for an online order: bought on Shopify, waiting to be picked up. Its own
-- migration, because Postgres can't use a new enum value in the transaction that adds it.

-- +goose Up
ALTER TYPE plant_status ADD VALUE IF NOT EXISTS 'held' AFTER 'for_sale'; -- IF NOT EXISTS: Down can't remove it
ALTER TYPE plant_event_type ADD VALUE IF NOT EXISTS 'held_for_order';
ALTER TYPE plant_event_type ADD VALUE IF NOT EXISTS 'released_from_order';

-- +goose Down
-- Postgres can't remove values from an enum; 00021's Down already put held plants back on sale.
SELECT 1;
