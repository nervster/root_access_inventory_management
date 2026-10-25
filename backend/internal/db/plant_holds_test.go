package db_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestPlantHolds checks the rule for choosing plants: a plant has at most one live hold.
func TestPlantHolds(t *testing.T) {
	tx := dbtest.Tx(t)
	ctx := t.Context()
	orgID, potSizeID, batchID := inventoryNursery(t, tx, "plant-holds")
	if err := addPlant(t, tx, orgID, batchID, potSizeID, "RAH-ADS-000001"); err != nil {
		t.Fatal(err)
	}
	var plantID int64
	if err := tx.QueryRow(ctx, `SELECT id FROM plants WHERE barcode = 'RAH-ADS-000001'`).Scan(&plantID); err != nil {
		t.Fatal(err)
	}

	hold := func(tx pgx.Tx, cart string) error {
		_, err := tx.Exec(ctx, `INSERT INTO plant_holds (organization_id, plant_id, cart_token, expires_at)
			VALUES ($1, $2, $3, now() + interval '30 minutes')`, orgID, plantID, cart)
		return err
	}
	if err := hold(tx, "cart-a"); err != nil {
		t.Fatal(err)
	}
	expectError(t, tx, "a second live hold on the same plant", func(tx pgx.Tx) error { return hold(tx, "cart-b") })

	// Once the first hold has run out, another cart can hold the plant.
	if _, err := tx.Exec(ctx, `UPDATE plant_holds SET status = 'expired' WHERE cart_token = 'cart-a'`); err != nil {
		t.Fatal(err)
	}
	if err := hold(tx, "cart-b"); err != nil {
		t.Errorf("holding a plant whose hold expired: %v", err)
	}
}
