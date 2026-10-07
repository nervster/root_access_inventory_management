package db_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// inventoryNursery creates a nursery with one pot size and one batch, and returns their IDs.
func inventoryNursery(t *testing.T, tx pgx.Tx, slug string) (orgID, potSizeID, batchID int64) {
	t.Helper()
	ctx := t.Context()
	org, err := dbgen.New(tx).CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
	if err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `INSERT INTO pot_sizes (organization_id, name, sellable) VALUES ($1, 'Tissue Culture Cup', false) RETURNING id`, org.ID).Scan(&potSizeID)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO batches (organization_id, code, plant_name_id, received_on)
		SELECT $1, 'B-0001', id, current_date FROM plant_names WHERE name = 'Dragon Scale' AND organization_id IS NULL
		RETURNING id`, org.ID).Scan(&batchID)
	if err != nil {
		t.Fatal(err)
	}
	return org.ID, potSizeID, batchID
}

func addPlant(t *testing.T, tx pgx.Tx, orgID, batchID, potSizeID int64, barcode string) error {
	t.Helper()
	_, err := tx.Exec(t.Context(), `
		INSERT INTO plants (organization_id, barcode, batch_id, plant_name_id, pot_size_id)
		SELECT $1, $2, $3, plant_name_id, $4 FROM batches WHERE id = $3`, orgID, barcode, batchID, potSizeID)
	return err
}

// expectError runs fn inside a savepoint (so a failed statement doesn't end the test's transaction)
// and fails the test if fn succeeds.
func expectError(t *testing.T, tx pgx.Tx, what string, fn func(pgx.Tx) error) {
	t.Helper()
	err := pgx.BeginFunc(t.Context(), tx, fn)
	if err == nil {
		t.Errorf("%s succeeded, want it rejected", what)
	}
}

func TestInventoryRules(t *testing.T) {
	tx := dbtest.Tx(t)
	ctx := t.Context()
	a, aPot, aBatch := inventoryNursery(t, tx, "inventory-a")
	b, bPot, bBatch := inventoryNursery(t, tx, "inventory-b")

	if err := addPlant(t, tx, a, aBatch, aPot, "TC-000001"); err != nil {
		t.Fatal(err)
	}
	// The same barcode in another nursery is fine; each nursery numbers its own plants.
	if err := addPlant(t, tx, b, bBatch, bPot, "TC-000001"); err != nil {
		t.Errorf("same barcode in another nursery: %v", err)
	}

	expectError(t, tx, "a duplicate barcode in one nursery", func(tx pgx.Tx) error {
		return addPlant(t, tx, a, aBatch, aPot, "TC-000001")
	})
	// Even code that skips Row-Level Security can't link one nursery's rows to another's.
	expectError(t, tx, "a plant in nursery A using nursery B's batch", func(tx pgx.Tx) error {
		return addPlant(t, tx, a, bBatch, aPot, "TC-000002")
	})
	expectError(t, tx, "a plant in nursery A using nursery B's pot size", func(tx pgx.Tx) error {
		return addPlant(t, tx, a, aBatch, bPot, "TC-000003")
	})

	// One product per plant name + variegation, including "no variegation".
	var productID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO products (organization_id, plant_name_id, title)
		SELECT $1, plant_name_id, 'Alocasia Dragon Scale' FROM batches WHERE id = $2 RETURNING id`, a, aBatch).Scan(&productID)
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, tx, "a second product for the same plant with no variegation", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO products (organization_id, plant_name_id, title)
			SELECT $1, plant_name_id, 'Duplicate' FROM batches WHERE id = $2`, a, aBatch)
		return err
	})

	// One variant per pot size, with a price that can't be negative.
	if _, err := tx.Exec(ctx, `INSERT INTO product_variants (organization_id, product_id, pot_size_id, sku, price_cents)
		VALUES ($1, $2, $3, 'DS-TC', 4500)`, a, productID, aPot); err != nil {
		t.Fatal(err)
	}
	expectError(t, tx, "a second variant for the same pot size", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO product_variants (organization_id, product_id, pot_size_id, sku, price_cents)
			VALUES ($1, $2, $3, 'DS-TC-2', 4500)`, a, productID, aPot)
		return err
	})
	expectError(t, tx, "a negative price", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE product_variants SET price_cents = -1 WHERE sku = 'DS-TC'`)
		return err
	})
}
