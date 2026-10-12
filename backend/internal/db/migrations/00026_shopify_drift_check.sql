-- The nightly drift check: the worker compares each store with Plant Stock (products, SKUs, prices,
-- stock), queues syncs to fix any difference, and records what it found for Settings.

-- +goose Up
ALTER TABLE shopify_connections
    ADD COLUMN drift_checked_at  timestamptz,                 -- when the last check started
    ADD COLUMN drift_differences jsonb NOT NULL DEFAULT '[]'; -- what it found (and fixed), in words

-- +goose Down
ALTER TABLE shopify_connections DROP COLUMN drift_differences, DROP COLUMN drift_checked_at;
