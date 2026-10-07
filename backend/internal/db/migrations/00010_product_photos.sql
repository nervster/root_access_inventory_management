-- A product's images (its Shopify images), in order: chosen from photos of plants of that kind.
-- Deleting the photo or the product removes the link.

-- +goose Up
CREATE TABLE product_photos (
    organization_id bigint  NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    product_id      bigint  NOT NULL,
    photo_id        bigint  NOT NULL,
    position        integer NOT NULL, -- 0 is the main image
    PRIMARY KEY (product_id, photo_id),
    UNIQUE (product_id, position),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, photo_id) REFERENCES plant_photos (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX product_photos_photo_idx ON product_photos (photo_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON product_photos TO nms_tenant;
ALTER TABLE product_photos ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_photos
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE product_photos;
