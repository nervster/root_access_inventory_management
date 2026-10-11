-- A refund without a return is allowed only as an approved special case: an Owner approves it with
-- a note saying why (e.g. "arrived damaged, photo checked"). Approvals stay listed, to look back on.

-- +goose Up
ALTER TABLE refunds RENAME COLUMN reviewed_at TO approved_at;
ALTER TABLE refunds RENAME COLUMN reviewed_by_name TO approved_by_name;
ALTER TABLE refunds ADD COLUMN approval_note text;
UPDATE refunds SET approval_note = 'Reviewed (before approvals needed a note)' WHERE approved_at IS NOT NULL;
ALTER TABLE refunds ADD CONSTRAINT refunds_approval_has_a_note CHECK (approved_at IS NULL OR approval_note IS NOT NULL);
DROP INDEX refunds_unreviewed_idx;
CREATE INDEX refunds_unapproved_idx ON refunds (organization_id) WHERE shopify_return_id IS NULL AND approved_at IS NULL;

-- +goose Down
DROP INDEX refunds_unapproved_idx;
CREATE INDEX refunds_unreviewed_idx ON refunds (organization_id) WHERE shopify_return_id IS NULL AND approved_at IS NULL;
ALTER TABLE refunds DROP CONSTRAINT refunds_approval_has_a_note;
ALTER TABLE refunds DROP COLUMN approval_note;
ALTER TABLE refunds RENAME COLUMN approved_by_name TO reviewed_by_name;
ALTER TABLE refunds RENAME COLUMN approved_at TO reviewed_at;
