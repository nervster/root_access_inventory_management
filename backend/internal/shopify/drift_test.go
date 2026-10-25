package shopify

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// syncedProduct puts a product on the store, with stock at the Greenhouse: 2 Small Cups ($12) and
// 1 Medium Cup ($20) for sale.
func (s *shopTest) syncedProduct() int64 {
	s.t.Helper()
	s.connect()
	s.chooseLocation(greenhouse)
	product, _ := s.addProduct(0, "Checked", map[string]*int{"Small Cup": cents(1200), "Medium Cup": cents(2000)})
	s.addPlants(product, "Small Cup", "for_sale", 2)
	s.addPlants(product, "Medium Cup", "for_sale", 1)
	s.sync(product)
	if err := s.syncStock(product); err != nil {
		s.t.Fatal(err)
	}
	return product
}

// checkDrift runs the check (with no jobs waiting before it) and returns what it found.
func (s *shopTest) checkDrift() []string {
	s.t.Helper()
	if _, err := s.db.Exec(s.t.Context(), "DELETE FROM jobs"); err != nil {
		s.t.Fatal(err)
	}
	if err := s.store.CheckDrift(s.t.Context(), s.org.ID); err != nil {
		s.t.Fatalf("drift check: %v", err)
	}
	got := s.connection("owner")
	if got.DriftCheckedAt == nil {
		s.t.Fatal("the check wasn't recorded")
	}
	return got.DriftFound
}

func (s *shopTest) variantOnStore(pot string) *fakeVariant {
	s.t.Helper()
	product := s.onlyProduct()
	for i := range product.Variants {
		if product.Variants[i].PotSize == pot {
			return &product.Variants[i]
		}
	}
	s.t.Fatalf("no %s on the store", pot)
	return nil
}

func TestDriftCheck(t *testing.T) {
	s := newShopTest(t)
	product := s.syncedProduct()

	// Everything matches: nothing to fix.
	if found := s.checkDrift(); len(found) != 0 || s.waitingJobs(JobSyncProduct, 0)+s.waitingJobs(JobSyncStock, 0) != 0 {
		t.Fatalf("found %v, want nothing", found)
	}

	// Stock edited in Shopify: a stock sync puts it right.
	medium := s.variantOnStore("Medium Cup")
	s.fake.onHand[medium.InventoryItemID+"@"+greenhouse] = 5
	found := s.checkDrift()
	if fmt.Sprint(found) != "[Checked: P0-MediumCup has 5 on hand on Shopify, 1 here]" ||
		s.waitingJobs(JobSyncStock, product) != 1 || s.waitingJobs(JobSyncProduct, product) != 0 {
		t.Errorf("found %v (product syncs %d, stock syncs %d), want the stock difference and a stock sync",
			found, s.waitingJobs(JobSyncProduct, product), s.waitingJobs(JobSyncStock, product))
	}
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}

	// A price edited in Shopify, and an SKU added there: a product sync.
	s.variantOnStore("Small Cup").Price = "10.0"
	s.onlyProduct().Variants = append(s.onlyProduct().Variants, fakeVariant{ID: "gid://shopify/ProductVariant/77", SKU: "EXTRA", Price: "5.00", PotSize: "Huge"})
	found = s.checkDrift()
	if !slices.Contains(found, "Checked: P0-SmallCup costs $10.0 on Shopify, $12.00 here") ||
		!slices.Contains(found, "Checked: SKU EXTRA is on Shopify but not for sale here") || s.waitingJobs(JobSyncProduct, product) != 1 {
		t.Errorf("found %v (product syncs %d), want the price and extra SKU, and a product sync", found, s.waitingJobs(JobSyncProduct, product))
	}
	s.sync(product)
	if found := s.checkDrift(); len(found) != 0 {
		t.Errorf("after syncing: %v, want everything matching again", found)
	}

	// Deleted in Shopify: synced again (as a draft).
	delete(s.fake.products, s.onlyProduct().ID)
	if found := s.checkDrift(); len(found) != 1 || !strings.Contains(found[0], "deleted on Shopify") || s.waitingJobs(JobSyncProduct, product) != 1 {
		t.Errorf("found %v, want the deleted product and a product sync", found)
	}

	// A priced product that never reached Shopify (e.g. its sync gave up).
	other, _ := s.addProduct(1, "Never Synced", map[string]*int{"Small Cup": cents(900)})
	if found := s.checkDrift(); !slices.Contains(found, "Never Synced: not on Shopify yet") || s.waitingJobs(JobSyncProduct, other) != 1 {
		t.Errorf("found %v, want the unsynced product and a product sync", found)
	}

	s.expect("owner", "POST", "/api/orgs/{org}/shopify/check", "", 202)
	s.expect("admin", "POST", "/api/orgs/{org}/shopify/check", "", 403)
}

// TestDriftCheckRunsAt3am: it's queued when it's 3 am in the nursery's time zone, once a night.
func TestDriftCheckRunsAt3am(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	queued := func() int {
		t.Helper()
		if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
			t.Fatal(err)
		}
		if err := QueueDriftChecks(t.Context(), s.db); err != nil {
			t.Fatal(err)
		}
		return s.waitingJobs(JobCheckDrift, 0)
	}
	setZone := func(localHour int) {
		t.Helper()
		// Etc/GMT+N is N hours behind UTC; pick N so it's localHour there now.
		offset := time.Now().UTC().Hour() - localHour
		if offset > 12 {
			offset -= 24
		} else if offset < -14 {
			offset += 24
		}
		zone := fmt.Sprintf("Etc/GMT+%d", offset)
		if offset < 0 {
			zone = fmt.Sprintf("Etc/GMT-%d", -offset)
		}
		if _, err := s.db.Exec(t.Context(), "UPDATE organizations SET time_zone = $1 WHERE id = $2", zone, s.org.ID); err != nil {
			t.Fatal(err)
		}
	}

	setZone(14)
	if n := queued(); n != 0 {
		t.Errorf("at 2 pm: %d checks queued, want none", n)
	}
	setZone(3)
	if n := queued(); n != 1 {
		t.Errorf("at 3 am: %d checks queued, want 1", n)
	}
	if err := s.store.CheckDrift(t.Context(), s.org.ID); err != nil {
		t.Fatal(err)
	}
	if n := queued(); n != 0 {
		t.Errorf("after tonight's check: %d queued, want none until tomorrow", n)
	}
}
