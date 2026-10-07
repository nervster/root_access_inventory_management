-- Running numbers for each nursery's barcodes (RAH-000123) and batch codes (B-0007). Reserving
-- numbers updates this one row, which Postgres locks, so two people adding batches at once never
-- get the same numbers.

-- +goose Up
CREATE TABLE inventory_counters (
    organization_id   bigint PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    barcode_prefix    text   NOT NULL, -- "RAH", from the nursery's slug when first used
    next_plant_number bigint NOT NULL DEFAULT 1,
    next_batch_number bigint NOT NULL DEFAULT 1
);

GRANT SELECT, INSERT, UPDATE ON inventory_counters TO nms_tenant;
ALTER TABLE inventory_counters ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON inventory_counters
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE inventory_counters;
