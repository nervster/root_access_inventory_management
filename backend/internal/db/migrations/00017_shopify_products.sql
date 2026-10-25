-- Pushing products to Shopify. Each product (with its priced variants) becomes a Shopify product;
-- these tables remember which one, per store, so the next sync updates it instead of adding another.
-- Triggers queue a sync whenever a product, its variants, or a pot size's name or order changes,
-- for nurseries with a connected store, so no part of the app can forget to.

-- +goose Up
CREATE TABLE shopify_products (
    organization_id    bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    product_id         bigint      NOT NULL,
    shop               text        NOT NULL, -- a different store starts over
    shopify_product_id text,                 -- gid://shopify/Product/123; NULL until created
    synced_at          timestamptz,
    sync_error         text,                 -- why the last sync failed (Shopify refused the product)
    PRIMARY KEY (product_id, shop),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE CASCADE
);

-- For the composite foreign key below (keeps a variant's Shopify ID inside its nursery).
ALTER TABLE product_variants ADD CONSTRAINT product_variants_organization_id_id_key UNIQUE (organization_id, id);

CREATE TABLE shopify_variants (
    organization_id           bigint NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    variant_id                bigint NOT NULL,
    shop                      text   NOT NULL,
    shopify_variant_id        text   NOT NULL, -- gid://shopify/ProductVariant/123
    shopify_inventory_item_id text   NOT NULL, -- gid://shopify/InventoryItem/123, for stock
    PRIMARY KEY (variant_id, shop),
    FOREIGN KEY (organization_id, variant_id) REFERENCES product_variants (organization_id, id) ON DELETE CASCADE
);

GRANT SELECT, INSERT, UPDATE, DELETE ON shopify_products, shopify_variants TO nms_tenant;
ALTER TABLE shopify_products ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shopify_products
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE shopify_variants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shopify_variants
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- The same job waiting twice does nothing extra (a sync makes Shopify match the product as it is
-- when it runs), so a waiting duplicate isn't added: saving 3 prices queues 1 sync.
CREATE UNIQUE INDEX jobs_waiting_once_idx ON jobs (organization_id, kind, payload) WHERE sent_at IS NULL;

-- Queues a sync of product_id, if the nursery has a store connected.
CREATE FUNCTION queue_shopify_product_sync(org bigint, product bigint) RETURNS void
    LANGUAGE sql
    AS $$
        INSERT INTO jobs (organization_id, kind, payload)
        SELECT org, 'shopify.sync_product', jsonb_build_object('productId', product)
        WHERE EXISTS (SELECT 1 FROM shopify_connections WHERE organization_id = org)
        ON CONFLICT DO NOTHING
    $$;

-- +goose StatementBegin
CREATE FUNCTION queue_shopify_sync_on_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    CASE TG_TABLE_NAME
        WHEN 'products' THEN
            PERFORM queue_shopify_product_sync(NEW.organization_id, NEW.id);
        WHEN 'product_variants' THEN
            IF TG_OP = 'DELETE' THEN
                PERFORM queue_shopify_product_sync(OLD.organization_id, OLD.product_id);
                RETURN OLD;
            END IF;
            PERFORM queue_shopify_product_sync(NEW.organization_id, NEW.product_id);
        WHEN 'pot_sizes' THEN -- names and order of the "Pot size" option
            PERFORM queue_shopify_product_sync(v.organization_id, v.product_id)
            FROM product_variants v
            WHERE v.pot_size_id = NEW.id
            GROUP BY v.organization_id, v.product_id;
    END CASE;
    RETURN NEW;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shopify_sync_on_product_change
    AFTER INSERT OR UPDATE OF title, description, product_type, tags ON products
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_sync_on_change();
CREATE TRIGGER shopify_sync_on_variant_change
    AFTER INSERT OR DELETE OR UPDATE OF sku, price_cents, pot_size_id ON product_variants
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_sync_on_change();
CREATE TRIGGER shopify_sync_on_pot_size_change
    AFTER UPDATE OF name, sort_order ON pot_sizes
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_sync_on_change();

-- +goose Down
DROP TRIGGER shopify_sync_on_pot_size_change ON pot_sizes;
DROP TRIGGER shopify_sync_on_variant_change ON product_variants;
DROP TRIGGER shopify_sync_on_product_change ON products;
DROP FUNCTION queue_shopify_sync_on_change();
DROP FUNCTION queue_shopify_product_sync(bigint, bigint);
DROP INDEX jobs_waiting_once_idx;
DROP TABLE shopify_variants;
ALTER TABLE product_variants DROP CONSTRAINT product_variants_organization_id_id_key;
DROP TABLE shopify_products;
