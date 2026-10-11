-- Pushing product images to Shopify: a product's chosen photos (product_photos, in order) become its
-- images there. This remembers which Shopify image each photo became, per store, so the next sync
-- keeps it instead of uploading it again. Changing a product's photos queues a product sync.

-- +goose Up
CREATE TABLE shopify_media (
    organization_id  bigint NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    product_id       bigint NOT NULL,
    photo_id         bigint NOT NULL,
    shop             text   NOT NULL,
    shopify_media_id text   NOT NULL, -- gid://shopify/MediaImage/123
    PRIMARY KEY (product_id, photo_id, shop),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, photo_id) REFERENCES plant_photos (organization_id, id) ON DELETE CASCADE
);
GRANT SELECT, INSERT, UPDATE, DELETE ON shopify_media TO nms_tenant;
ALTER TABLE shopify_media ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shopify_media
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose StatementBegin
CREATE FUNCTION queue_shopify_sync_on_photo_change() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM queue_shopify_product_sync(OLD.organization_id, OLD.product_id);
    ELSE
        PERFORM queue_shopify_product_sync(NEW.organization_id, NEW.product_id);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE TRIGGER shopify_sync_on_product_photo_change
    AFTER INSERT OR UPDATE OR DELETE ON product_photos
    FOR EACH ROW EXECUTE FUNCTION queue_shopify_sync_on_photo_change();

-- +goose Down
DROP TRIGGER shopify_sync_on_product_photo_change ON product_photos;
DROP FUNCTION queue_shopify_sync_on_photo_change();
DROP TABLE shopify_media;
