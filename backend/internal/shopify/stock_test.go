package shopify

import (
	"fmt"
	"testing"
)

const greenhouse, marketStall = "gid://shopify/Location/1", "gid://shopify/Location/2"

// addPlants adds count plants of the product (its name and variegation) in the pot, with status.
func (s *shopTest) addPlants(productID int64, pot, status string, count int) []string {
	s.t.Helper()
	ctx := s.t.Context()
	var batchID int64
	err := s.db.QueryRow(ctx, `
		INSERT INTO batches (organization_id, code, plant_name_id, received_on)
		SELECT organization_id, 'B-' || (random() * 1e9)::bigint, plant_name_id, current_date FROM products WHERE id = $1
		RETURNING id`, productID).Scan(&batchID)
	if err != nil {
		s.t.Fatal(err)
	}
	var barcodes []string
	for range count {
		var barcode string
		err := s.db.QueryRow(ctx, `
			INSERT INTO plants (organization_id, barcode, batch_id, plant_name_id, variegation_id, pot_size_id, status)
			SELECT p.organization_id, 'ST-' || (random() * 1e9)::bigint, $2, p.plant_name_id, p.variegation_id, ps.id, $4::plant_status
			FROM products p JOIN pot_sizes ps ON ps.organization_id = p.organization_id AND ps.name = $3
			WHERE p.id = $1
			RETURNING barcode`, productID, batchID, pot, status).Scan(&barcode)
		if err != nil {
			s.t.Fatal(err)
		}
		barcodes = append(barcodes, barcode)
	}
	return barcodes
}

func (s *shopTest) setPlant(barcode, set string) {
	s.t.Helper()
	if _, err := s.db.Exec(s.t.Context(), "UPDATE plants SET "+set+" WHERE barcode = $1", barcode); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shopTest) syncStock(productID int64) error {
	s.t.Helper()
	return s.store.SyncStock(s.t.Context(), s.org.ID, productID)
}

// onHand is the store's on-hand count of the product's SKU in the pot, at location.
func (s *shopTest) onHand(pot, location string) int {
	s.t.Helper()
	for _, v := range s.onlyProduct().Variants {
		if v.PotSize == pot {
			return s.fake.onHand[v.InventoryItemID+"@"+location]
		}
	}
	s.t.Fatalf("no %s on the store", pot)
	return 0
}

func (s *shopTest) chooseLocation(id string) {
	s.t.Helper()
	s.expect("owner", "PUT", "/api/orgs/{org}/shopify/location", fmt.Sprintf(`{"locationId":%q}`, id), 200)
}

func TestSyncStock(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Stocked", map[string]*int{"Small Cup": cents(1200), "Medium Cup": cents(2000)})
	small := s.addPlants(product, "Small Cup", "for_sale", 2)
	growing := s.addPlants(product, "Small Cup", "growing", 1)
	s.addPlants(product, "Medium Cup", "for_sale", 1)
	s.sync(product)

	// No pickup location chosen yet: nothing to set.
	if err := s.syncStock(product); err != nil || s.fake.activations != 0 {
		t.Fatalf("without a location: %v, %d activations; want nothing done", err, s.fake.activations)
	}

	// Choosing it puts each SKU's for-sale plants there.
	s.chooseLocation(greenhouse)
	if s.waitingJobs(JobSyncAll, 0) != 1 {
		t.Error("choosing a location didn't queue a sync of everything")
	}
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}
	if s.onHand("Small Cup", greenhouse) != 2 || s.onHand("Medium Cup", greenhouse) != 1 || s.fake.activations != 2 {
		t.Errorf("on hand: Small %d, Medium %d (%d activations); want 2 and 1, each SKU stocked once",
			s.onHand("Small Cup", greenhouse), s.onHand("Medium Cup", greenhouse), s.fake.activations)
	}

	// Marking another for sale, then selling one: the count follows; SKUs aren't stocked again.
	s.setPlant(growing[0], "status = 'for_sale'")
	s.setPlant(small[0], "status = 'sold'")
	s.sync(product) // a product sync keeps where the stock is kept
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}
	if s.onHand("Small Cup", greenhouse) != 2 || s.fake.activations != 2 {
		t.Errorf("Small Cup on hand %d (%d activations), want 2 without stocking again", s.onHand("Small Cup", greenhouse), s.fake.activations)
	}

	// Moving the pickup location moves the stock: none left at the old one.
	s.chooseLocation(marketStall)
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}
	if s.onHand("Small Cup", marketStall) != 2 || s.onHand("Small Cup", greenhouse) != 0 || s.onHand("Medium Cup", greenhouse) != 0 {
		t.Errorf("after moving: Market Stall %d, Greenhouse %d/%d; want 2 at the new one and 0 at the old",
			s.onHand("Small Cup", marketStall), s.onHand("Small Cup", greenhouse), s.onHand("Medium Cup", greenhouse))
	}
}

func TestStockRecoversFromShopifyChanges(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	s.chooseLocation(greenhouse)
	product, _ := s.addProduct(0, "Changed In Shopify", map[string]*int{"Small Cup": cents(1200)})
	s.addPlants(product, "Small Cup", "for_sale", 3)
	s.sync(product)
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}

	// Someone stops stocking it at the location in Shopify: this sync fails, the retry fixes it.
	clear(s.fake.stocked)
	if err := s.syncStock(product); err == nil {
		t.Fatal("want an error (and a retry) when the SKU isn't stocked at the location")
	}
	if err := s.syncStock(product); err != nil || s.onHand("Small Cup", greenhouse) != 3 {
		t.Fatalf("retry: %v, on hand %d; want it stocked again with 3", err, s.onHand("Small Cup", greenhouse))
	}

	// Someone deletes the SKU in Shopify: a product sync is queued to create it again.
	s.onlyProduct().Variants = nil
	if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}
	if err := s.syncStock(product); err != nil || s.waitingJobs(JobSyncProduct, product) != 1 {
		t.Errorf("after deleting the SKU: %v, %d product syncs queued; want 1", err, s.waitingJobs(JobSyncProduct, product))
	}
}

func TestPlantChangesQueueStockSyncs(t *testing.T) {
	s := newShopTest(t)
	product, _ := s.addProduct(0, "Watched", map[string]*int{"Small Cup": cents(1200), "Medium Cup": cents(2000)})
	plants := s.addPlants(product, "Small Cup", "growing", 3)
	s.setPlant(plants[0], "status = 'for_sale'")
	if n := s.waitingJobs(JobSyncStock, product); n != 0 {
		t.Fatalf("%d stock syncs without a store, want none", n)
	}
	s.connect()

	for _, change := range []struct {
		name, barcode, set string
		wantSync           bool
	}{
		{"goes on sale", plants[1], "status = 'for_sale'", true},
		{"for-sale plant repotted", plants[1], "pot_size_id = (SELECT id FROM pot_sizes WHERE name = 'Medium Cup')", true},
		{"for-sale plant discarded", plants[0], "status = 'discarded'", true},
		{"growing plant repotted", plants[2], "pot_size_id = (SELECT id FROM pot_sizes WHERE name = 'Medium Cup')", false},
		{"notes on a for-sale plant", plants[1], "notes = 'Pest free'", false},
	} {
		if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
			t.Fatal(err)
		}
		s.setPlant(change.barcode, change.set)
		if got := s.waitingJobs(JobSyncStock, product) == 1; got != change.wantSync {
			t.Errorf("%s: stock sync queued = %v, want %v", change.name, got, change.wantSync)
		}
	}
}
