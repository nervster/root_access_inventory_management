-- Tissue-culture inventory: batches arrive, each plant gets a barcode and moves up through pot sizes,
-- and plants in a sellable pot can be marked for sale. Products and their variants (one per pot
-- size, like Shopify's) set the SKU and price; a variant's stock is counted from the plants.
--
-- Every table here belongs to one nursery: Row-Level Security like migration 00002, plus composite
-- foreign keys (organization_id, id) so a row can only point at rows of the same nursery.
-- Money is stored in whole cents ($75.00 = 7500) to avoid rounding errors.

-- +goose Up
CREATE TYPE plant_status AS ENUM ('growing', 'for_sale', 'sold', 'discarded');

-- The nursery's own pot sizes, smallest first.
CREATE TABLE pot_sizes (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name            text        NOT NULL,                 -- "Small Cup"
    diameter_inches double precision CHECK (diameter_inches > 0),
    height_inches   double precision CHECK (height_inches > 0),
    sort_order      integer     NOT NULL DEFAULT 0,       -- smallest first
    sellable        boolean     NOT NULL DEFAULT true,    -- false for Tissue Culture Cup: grow on first
    description     text,                                 -- for the Shopify collection of this size
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name),
    UNIQUE (organization_id, id)
);

-- A group of plants that arrived together, e.g. 30 tissue-culture plantlets from one lab.
CREATE TABLE batches (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id  bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    code             text        NOT NULL,                -- "B-0007", printed on labels
    plant_name_id    bigint      NOT NULL REFERENCES plant_names (id),
    variegation_id   bigint      REFERENCES variegations (id),
    source           text,                                -- the lab or seller
    received_on      date        NOT NULL,
    unit_cost_cents  integer     CHECK (unit_cost_cents >= 0), -- what each plant cost
    notes            text,
    created_by       bigint      REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, code),
    UNIQUE (organization_id, id)
);

-- One physical plant. Its name and variegation start as the batch's and can be corrected per plant
-- (a tissue-culture plant can lose its variegation).
CREATE TABLE plants (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint       NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    barcode         text         NOT NULL,                -- made by NMS, printed on the plant's label
    batch_id        bigint       NOT NULL,
    plant_name_id   bigint       NOT NULL REFERENCES plant_names (id),
    variegation_id  bigint       REFERENCES variegations (id),
    pot_size_id     bigint       NOT NULL,
    status          plant_status NOT NULL DEFAULT 'growing',
    notes           text,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    UNIQUE (organization_id, barcode),
    UNIQUE (organization_id, id),
    FOREIGN KEY (organization_id, batch_id) REFERENCES batches (organization_id, id),
    FOREIGN KEY (organization_id, pot_size_id) REFERENCES pot_sizes (organization_id, id)
);
CREATE INDEX plants_batch_idx ON plants (batch_id);
-- Counting a variant's stock: plants of one name, variegation, and pot size that are for sale.
CREATE INDEX plants_stock_idx ON plants (organization_id, plant_name_id, variegation_id, pot_size_id, status);

-- Every move to a new pot: the plant's growth history. Usually a whole batch at once.
CREATE TABLE pot_changes (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id  bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_id         bigint      NOT NULL,
    from_pot_size_id bigint,                              -- NULL for the plant's first pot
    to_pot_size_id   bigint      NOT NULL,
    changed_at       timestamptz NOT NULL DEFAULT now(),
    changed_by       bigint      REFERENCES users (id) ON DELETE SET NULL,
    FOREIGN KEY (organization_id, plant_id) REFERENCES plants (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, from_pot_size_id) REFERENCES pot_sizes (organization_id, id),
    FOREIGN KEY (organization_id, to_pot_size_id) REFERENCES pot_sizes (organization_id, id)
);
CREATE INDEX pot_changes_plant_idx ON pot_changes (plant_id, changed_at);

-- What customers buy: one product per plant name + variegation (a Shopify product)...
CREATE TABLE products (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    plant_name_id   bigint      NOT NULL REFERENCES plant_names (id),
    variegation_id  bigint      REFERENCES variegations (id),
    title           text        NOT NULL,                 -- "Alocasia Dragon Scale Aurea Variegated"
    description     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (organization_id, plant_name_id, variegation_id),
    UNIQUE (organization_id, id)
);

-- ...with one variant per pot size (a Shopify variant): its own SKU and price.
CREATE TABLE product_variants (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    organization_id bigint      NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    product_id      bigint      NOT NULL,
    pot_size_id     bigint      NOT NULL,
    sku             text        NOT NULL,
    price_cents     integer     NOT NULL CHECK (price_cents >= 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, sku),
    UNIQUE (product_id, pot_size_id),
    FOREIGN KEY (organization_id, product_id) REFERENCES products (organization_id, id) ON DELETE CASCADE,
    FOREIGN KEY (organization_id, pot_size_id) REFERENCES pot_sizes (organization_id, id)
);

GRANT SELECT, INSERT, UPDATE, DELETE ON pot_sizes, batches, plants, pot_changes, products, product_variants TO nms_tenant;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO nms_tenant;

ALTER TABLE pot_sizes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pot_sizes
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE batches ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON batches
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE plants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON plants
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE pot_changes ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pot_changes
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON products
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());
ALTER TABLE product_variants ENABLE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_variants
    USING (organization_id = current_organization_id())
    WITH CHECK (organization_id = current_organization_id());

-- +goose Down
DROP TABLE product_variants;
DROP TABLE products;
DROP TABLE pot_changes;
DROP TABLE plants;
DROP TABLE batches;
DROP TABLE pot_sizes;
DROP TYPE plant_status;
