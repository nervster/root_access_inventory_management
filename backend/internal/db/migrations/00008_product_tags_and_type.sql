-- Product fields sales channels use (Shopify): a product type ("Houseplant") and tags ("Aroid", "Rare")
-- for collections and search.

-- +goose Up
ALTER TABLE products
    ADD COLUMN product_type text,
    ADD COLUMN tags         text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE products
    DROP COLUMN tags,
    DROP COLUMN product_type;
