-- When and why a plant was discarded (died, pests, damaged…). The plant and its history stay.

-- +goose Up
ALTER TABLE plants
    ADD COLUMN discarded_at   timestamptz,
    ADD COLUMN discard_reason text;

-- +goose Down
ALTER TABLE plants
    DROP COLUMN discard_reason,
    DROP COLUMN discarded_at;
