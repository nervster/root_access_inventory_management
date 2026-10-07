package inventory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func (n *repotNursery) createProduct(as, body string, wantStatus int) productResponse {
	n.t.Helper()
	var product productResponse
	_ = json.Unmarshal(n.expect(as, "POST", "/api/orgs/{org}/products", body, wantStatus).Body.Bytes(), &product)
	return product
}

func (n *repotNursery) setPrice(productID, potSizeID int64, body string, wantStatus int) {
	n.t.Helper()
	n.expect("owner", "PUT", fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", productID, potSizeID), body, wantStatus)
}

func (n *repotNursery) markForSale(body string, wantStatus int) (result saleResponse, errorMessage string) {
	n.t.Helper()
	recorder := n.expect("owner", "POST", "/api/orgs/{org}/plants/mark-for-sale", body, wantStatus)
	if wantStatus == http.StatusOK {
		_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	} else {
		var e struct{ Error string }
		_ = json.Unmarshal(recorder.Body.Bytes(), &e)
		errorMessage = e.Error
	}
	return result, errorMessage
}

// stock returns the product's stock per pot size name, as a viewer sees it.
func (n *repotNursery) stock(productID int64) map[string]int64 {
	n.t.Helper()
	var product productResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", fmt.Sprintf("/api/orgs/{org}/products/%d", productID), "", 200).Body.Bytes(), &product)
	stock := map[string]int64{}
	for _, v := range product.Variants {
		stock[v.PotSize] = v.Stock
	}
	return stock
}

func TestProducts(t *testing.T) {
	n := newRepotNursery(t)
	dragonScale := n.plantNameID("Alocasia baginda 'Dragon Scale'")

	n.createProduct("staff", fmt.Sprintf(`{"plantNameId":%d}`, dragonScale), 403) // pricing is for listings.manage
	product := n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d}`, dragonScale), 201)
	if product.Title != "Alocasia baginda 'Dragon Scale'" || len(product.Variants) != 0 {
		t.Errorf("product = %+v, want it titled after the plant with no variants yet", product)
	}
	n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d,"title":"Again"}`, dragonScale), 409)

	var aurea int64
	if err := n.db.QueryRow(t.Context(), "SELECT id FROM variegations WHERE name = 'Aurea' AND organization_id IS NULL").Scan(&aurea); err != nil {
		t.Fatal(err)
	}
	variegated := n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d,"variegationId":%d}`, dragonScale, aurea), 201)
	if variegated.Title != "Alocasia baginda 'Dragon Scale' Aurea" {
		t.Errorf("title = %q, want the variegation added", variegated.Title)
	}

	// Prices: one per sellable pot size, SKUs unique in the nursery.
	n.setPrice(product.ID, n.tc.ID, `{"sku":"DS-TC","priceCents":2500}`, 400) // the Tissue Culture Cup can't be sold
	n.setPrice(product.ID, n.small.ID, `{"sku":"DS-S","priceCents":4500}`, 200)
	n.setPrice(product.ID, n.small.ID, `{"sku":"DS-S","priceCents":5000}`, 200) // a new price
	n.setPrice(variegated.ID, n.small.ID, `{"sku":"DS-S","priceCents":9000}`, 409)
	n.setPrice(product.ID, n.medium.ID, `{"sku":"DS-M","priceCents":-1}`, 400)
	n.setPrice(product.ID, 999999, `{"sku":"DS-X","priceCents":100}`, 404)

	var products []productResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/products", "", 200).Body.Bytes(), &products)
	if len(products) != 2 || products[0].ID != product.ID || len(products[0].Variants) != 1 || products[0].Variants[0].PriceCents != 5000 {
		t.Errorf("products = %+v, want the plain one first with its Small Cup at $50.00", products)
	}
	var rivals []productResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/products", "", 200).Body.Bytes(), &rivals)
	if len(rivals) != 0 {
		t.Errorf("the other nursery sees %+v, want none", rivals)
	}
}

// TestSellingFollowsThePlants walks a batch from the Tissue Culture Cup to sale, and checks the
// stock moves with the plants.
func TestSellingFollowsThePlants(t *testing.T) {
	n := newRepotNursery(t)
	product := n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d}`, n.plantNameID("Alocasia baginda 'Dragon Scale'")), 201)
	n.setPrice(product.ID, n.small.ID, `{"sku":"DS-S","priceCents":4500}`, 200)
	batch := fmt.Sprintf(`{"batchId":%d}`, n.batch.ID)

	// Still in the Tissue Culture Cup: nothing can be sold yet.
	if got, _ := n.markForSale(batch, 200); got.Marked != 0 || got.Skipped.UnsellablePot != 5 {
		t.Errorf("marking the batch in the TC cup: %+v, want all 5 skipped (pot can't be sold)", got)
	}

	// Grown on: the batch moves to the Small Cup, one plant to the Medium Cup (no price yet).
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000005"]}`, n.medium.ID), 200)
	if got, _ := n.markForSale(batch, 200); got.Marked != 4 || got.Skipped.NoPrice != 1 {
		t.Errorf("marking the batch: %+v, want 4 marked and 1 skipped (no Medium Cup price)", got)
	}
	if _, message := n.markForSale(`{"barcodes":["IT-000005"]}`, 400); !strings.Contains(message, "IT-000005") {
		t.Errorf("error = %q, want it to name IT-000005", message)
	}
	n.setPrice(product.ID, n.medium.ID, `{"sku":"DS-M","priceCents":7500}`, 200)
	if got, _ := n.markForSale(`{"barcodes":["IT-000005"]}`, 200); got.Marked != 1 {
		t.Errorf("marking IT-000005 once priced: %+v, want 1 marked", got)
	}
	if got := n.stock(product.ID); got["Small Cup"] != 4 || got["Medium Cup"] != 1 {
		t.Errorf("stock = %v, want 4 Small Cup and 1 Medium Cup", got)
	}
	if got, _ := n.markForSale(batch, 200); got.Marked != 0 || got.AlreadyForSale != 5 {
		t.Errorf("marking the batch again: %+v, want all 5 already for sale", got)
	}

	// A for-sale plant moved to a bigger pot moves to that pot's stock.
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000004"]}`, n.medium.ID), 200)
	if got := n.stock(product.ID); got["Small Cup"] != 3 || got["Medium Cup"] != 2 {
		t.Errorf("after repotting IT-000004: stock = %v, want 3 Small Cup and 2 Medium Cup", got)
	}

	// Taking plants off sale, and removing a price only once nothing is for sale at it.
	var unmarked struct{ Unmarked int }
	_ = json.Unmarshal(n.expect("owner", "POST", "/api/orgs/{org}/plants/unmark-for-sale", `{"barcodes":["IT-000004","IT-000005"]}`, 200).Body.Bytes(), &unmarked)
	if unmarked.Unmarked != 2 || n.stock(product.ID)["Medium Cup"] != 0 {
		t.Errorf("unmarked %d, Medium Cup stock %d; want 2 and 0", unmarked.Unmarked, n.stock(product.ID)["Medium Cup"])
	}
	n.expect("owner", "DELETE", fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", product.ID, n.small.ID), "", 409) // 3 still for sale
	n.expect("owner", "DELETE", fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", product.ID, n.medium.ID), "", 204)
}

func TestVariegationsAreSeparateProducts(t *testing.T) {
	n := newRepotNursery(t)
	dragonScale := n.plantNameID("Alocasia baginda 'Dragon Scale'")
	product := n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d}`, dragonScale), 201)
	n.setPrice(product.ID, n.small.ID, `{"sku":"DS-S","priceCents":4500}`, 200)

	// An Aurea batch of the same plant, straight into the Small Cup.
	var aurea int64
	if err := n.db.QueryRow(t.Context(), "SELECT id FROM variegations WHERE name = 'Aurea' AND organization_id IS NULL").Scan(&aurea); err != nil {
		t.Fatal(err)
	}
	aureaBatch := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"variegationId":%d,"potSizeId":%d,"quantity":2,"receivedOn":"2026-10-02"}`,
		dragonScale, aurea, n.small.ID), 201)

	// The plain product's price doesn't apply to the Aurea plants.
	if got, _ := n.markForSale(fmt.Sprintf(`{"batchId":%d}`, aureaBatch.Batch.ID), 200); got.Marked != 0 || got.Skipped.NoPrice != 2 {
		t.Errorf("marking the Aurea batch: %+v, want both skipped (no Aurea price)", got)
	}
	if got := n.stock(product.ID); got["Small Cup"] != 0 {
		t.Errorf("plain Dragon Scale stock = %v, want the Aurea plants not counted", got)
	}
}
