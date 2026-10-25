-- Choosing plants, step 5 (docs/plant-picker-storefront.md): an order line the shopper chose a plant for (its
-- _plant property) holds exactly that plant. The line keeps which plant was chosen, so staff can see it when it
-- couldn't be held (sold in person, discarded) and offer the customer a similar one.

-- +goose Up
ALTER TABLE order_items ADD COLUMN chosen_plant_id bigint,
    ADD FOREIGN KEY (organization_id, chosen_plant_id) REFERENCES plants (organization_id, id) ON DELETE SET NULL (chosen_plant_id);

-- +goose Down
ALTER TABLE order_items DROP COLUMN chosen_plant_id;
