-- Refunds issued in Shopify on picked-up orders. One tied to a Shopify return is the normal path
-- (the plant came back first); one without a return breaks the rule "no refund without the plant
-- back" (e.g. Refund clicked instead of Return), so it's flagged until someone reviews it.

-- +goose Up
CREATE TABLE refunds (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id   bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    order_id          bigint      NOT NULL,
    shopify_refund_id text        NOT NULL, -- gid://shopify/Refund/123
    shopify_return_id text,                 -- the return it refunds; NULL: refunded without a return
    refunded_at       timestamptz NOT NULL,
    amount_cents      integer,
    items             text,                 -- what was refunded, e.g. "1 × Dragon Scale Albo"; NULL: money only
    reviewed_at       timestamptz,          -- someone looked into a refund without a return
    reviewed_by_name  text,
    UNIQUE (organization_id, shopify_refund_id),
    FOREIGN KEY (organization_id, order_id) REFERENCES orders (organization_id, id) ON DELETE CASCADE
);
CREATE INDEX refunds_unreviewed_idx ON refunds (organization_id) WHERE shopify_return_id IS NULL AND reviewed_at IS NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON refunds TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;
ALTER TABLE refunds ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON refunds
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE refunds;
