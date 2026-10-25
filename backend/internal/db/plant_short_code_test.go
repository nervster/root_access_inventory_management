package db_test

import (
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestPlantShortCode checks the letters that go into a plant's barcode (RAH-ADF-000123).
func TestPlantShortCode(t *testing.T) {
	tx := dbtest.Tx(t)
	ctx := t.Context()

	for fullName, want := range map[string]string{
		"Anthurium 'Delta Force'":         "ADF",
		"Alocasia baginda 'Dragon Scale'": "ABDS",
		"Alocasia 'Polly'":                "AP",
		"Anthurium":                       "ANT",
		"Mo":                              "MO",
		"Philodendron erubescens 'Pink Princess' extra": "PEPP",
		"":      "PLT",
		"  '' ": "PLT",
	} {
		var got string
		if err := tx.QueryRow(ctx, "SELECT plant_short_code($1)", fullName).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("plant_short_code(%q) = %q, want %q", fullName, got, want)
		}
	}
}
