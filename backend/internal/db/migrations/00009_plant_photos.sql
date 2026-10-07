-- Photos of individual plants, taken over time: each plant's growth timeline. The files live in S3
-- (LocalStack locally) under orgs/<nursery>/plants/<plant>/; this table records them.

-- +goose Up
CREATE TABLE plant_photos (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_id        bigint      NOT NULL,
    storage_key     text        NOT NULL UNIQUE, -- orgs/12/plants/345/<uuid>.jpg
    taken_at        timestamptz NOT NULL DEFAULT now(),
    caption         text,
    uploaded_by     bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, plant_id) REFERENCES plants (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX plant_photos_plant_idx ON plant_photos (plant_id, taken_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON plant_photos TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE plant_photos ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plant_photos
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE plant_photos;
