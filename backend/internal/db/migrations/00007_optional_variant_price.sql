-- Marking plants for sale creates their product and pot-size variant automatically, before anyone
-- has set a price, so a variant's price is optional until then. Unpriced variants are flagged in the
-- app and kept off sales channels.

-- +goose Up
ALTER TABLE product_variants ALTER COLUMN price_cents DROP NOT NULL;

-- +goose Down
UPDATE product_variants SET price_cents = 0 WHERE price_cents IS NULL;
ALTER TABLE product_variants ALTER COLUMN price_cents SET NOT NULL;
