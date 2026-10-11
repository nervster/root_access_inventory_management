package inventory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

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

func (n *repotNursery) products() []productResponse {
	n.t.Helper()
	var products []productResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/products", "", 200).Body.Bytes(), &products)
	return products
}

// product returns the product with title, failing the test if there isn't one.
func (n *repotNursery) product(title string) productResponse {
	n.t.Helper()
	for _, p := range n.products() {
		if p.Title == title {
			return p
		}
	}
	n.t.Fatalf("no product titled %q in %+v", title, n.products())
	return productResponse{}
}

// stock returns a product's stock per pot size name.
func stock(p productResponse) map[string]int64 {
	stock := map[string]int64{}
	for _, v := range p.Variants {
		stock[v.PotSize] = v.Stock
	}
	return stock
}

func (n *repotNursery) setVariant(as string, productID, potSizeID int64, body string, wantStatus int) {
	n.t.Helper()
	n.expect(as, "PATCH", fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", productID, potSizeID), body, wantStatus)
}

const dragonScale = "Alocasia baginda 'Dragon Scale'"

// TestMarkForSaleCreatesProductsAndSKUs: plants go on sale as soon as they're in a sellable pot; the
// first time a kind of plant is sold in a pot, its product and SKU are created, with no price yet.
func TestMarkForSaleCreatesProductsAndSKUs(t *testing.T) {
	n := newRepotNursery(t) // IT-000001 to 5 in the Tissue Culture Cup
	batch := fmt.Sprintf(`{"batchId":%d}`, n.batch.ID)

	if got, _ := n.markForSale(batch, 200); got.Marked != 0 || got.Skipped.UnsellablePot != 5 || len(got.NewVariants) != 0 {
		t.Errorf("marking the batch in the TC cup: %+v, want all 5 skipped and no SKUs created", got)
	}
	if got := n.products(); len(got) != 0 {
		t.Errorf("products = %+v, want none before anything is for sale", got)
	}

	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000005"]}`, n.medium.ID), 200)
	got, _ := n.markForSale(batch, 200)
	var created []string
	for _, v := range got.NewVariants {
		created = append(created, v.PotSize+"="+v.SKU)
	}
	if got.Marked != 5 || strings.Join(created, ",") != "Medium Cup=ABDS-MC,Small Cup=ABDS-SC" {
		t.Errorf("marking the batch: marked %d, created %v; want 5 marked and SKUs ABDS-MC and ABDS-SC", got.Marked, created)
	}

	product := n.product(dragonScale)
	if s := stock(product); s["Small Cup"] != 4 || s["Medium Cup"] != 1 || len(product.Variants) != 2 || product.Variants[0].PriceCents != nil {
		t.Errorf("product = %+v, want Small Cup (4) and Medium Cup (1), unpriced", product)
	}
	if got, _ := n.markForSale(batch, 200); got.AlreadyForSale != 5 || len(got.NewVariants) != 0 {
		t.Errorf("marking again: %+v, want all 5 already for sale, nothing new", got)
	}
}

func TestNewPlantsJoinTheirProduct(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)

	// A second Dragon Scale batch, straight into the Small Cup: joins the existing SKU.
	second := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":3,"receivedOn":"2026-10-02"}`,
		n.plantNameID(dragonScale), n.small.ID), 201)
	if got, _ := n.markForSale(fmt.Sprintf(`{"batchId":%d}`, second.Batch.ID), 200); got.Marked != 3 || len(got.NewVariants) != 0 {
		t.Errorf("marking the second batch: %+v, want 3 marked into the existing SKU", got)
	}
	if s := stock(n.product(dragonScale)); s["Small Cup"] != 8 {
		t.Errorf("stock = %v, want 8 in the Small Cup", s)
	}
}

func TestMarkForSaleByBarcode(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.small.ID), 200)

	// One plant still in the TC cup: nothing changes, and it's named.
	if _, message := n.markForSale(`{"barcodes":["IT-000001","IT-000002"]}`, 400); !strings.Contains(message, "IT-000002") {
		t.Errorf("error = %q, want it to name IT-000002", message)
	}
	if got := n.products(); len(got) != 0 {
		t.Errorf("products = %+v, want none: the refused request created nothing", got)
	}
	if got, _ := n.markForSale(`{"barcodes":["IT-000001"]}`, 200); got.Marked != 1 || len(got.NewVariants) != 1 {
		t.Errorf("marking IT-000001: %+v, want 1 marked with a new SKU", got)
	}
	n.expect("staff", "POST", "/api/orgs/{org}/plants/mark-for-sale", `{"barcodes":["IT-000001"]}`, 403) // listings.manage
}

func TestVariegationsAreSeparateProducts(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)
	plain := n.product(dragonScale)

	// Rename the plain SKU to what the Aurea one would be called, to force a clash.
	n.setVariant("owner", plain.ID, n.small.ID, `{"sku":"ABDS-AUR-SC"}`, 200)

	var aurea int64
	if err := n.db.QueryRow(t.Context(), "SELECT id FROM variegations WHERE name = 'Aurea' AND organization_id IS NULL").Scan(&aurea); err != nil {
		t.Fatal(err)
	}
	batch := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"variegationId":%d,"potSizeId":%d,"quantity":2,"receivedOn":"2026-10-02"}`,
		n.plantNameID(dragonScale), aurea, n.small.ID), 201)
	got, _ := n.markForSale(fmt.Sprintf(`{"batchId":%d}`, batch.Batch.ID), 200)
	if len(got.NewVariants) != 1 || got.NewVariants[0].Product != dragonScale+" Aurea" || got.NewVariants[0].SKU != "ABDS-AUR-SC-2" {
		t.Errorf("new variants = %+v, want a separate Aurea product whose SKU avoids the taken ABDS-AUR-SC", got.NewVariants)
	}
	if s := stock(n.product(dragonScale)); s["Small Cup"] != 5 {
		t.Errorf("plain stock = %v, want the Aurea plants not counted", s)
	}
}

func TestPricesAndDetails(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)
	product := n.product(dragonScale)

	n.setVariant("staff", product.ID, n.small.ID, `{"priceCents":4500}`, 403) // listings.manage
	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":4500}`, 200)
	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":-1}`, 400)
	n.setVariant("owner", product.ID, n.medium.ID, `{"priceCents":100}`, 404) // never sold in that pot
	if v := n.product(dragonScale).Variants[0]; v.PriceCents == nil || *v.PriceCents != 4500 || v.SKU != "ABDS-SC" {
		t.Errorf("variant = %+v, want $45.00 with its SKU unchanged", v)
	}

	productPath := fmt.Sprintf("/api/orgs/{org}/products/%d", product.ID)
	n.expect("owner", "PATCH", productPath,
		`{"title":"Dragon Scale","description":"Silvery, scale-like leaves.","productType":" Houseplant ","tags":["Aroid"," Rare ","aroid",""]}`, 200)
	if p := n.product("Dragon Scale"); *p.Description != "Silvery, scale-like leaves." || *p.ProductType != "Houseplant" || strings.Join(p.Tags, ",") != "Aroid,Rare" {
		t.Errorf("product = %+v, want the description, type Houseplant, and tags Aroid, Rare (trimmed, no repeats)", p)
	}
	n.expect("owner", "PATCH", productPath, `{"description":"Updated."}`, 200) // leaving tags out keeps them
	if p := n.product("Dragon Scale"); strings.Join(p.Tags, ",") != "Aroid,Rare" {
		t.Errorf("tags = %v after an update without tags, want them kept", p.Tags)
	}
	n.expect("owner", "PATCH", productPath, `{"tags":[]}`, 200)
	if p := n.product("Dragon Scale"); len(p.Tags) != 0 || p.Tags == nil {
		t.Errorf("tags = %#v, want an empty list after clearing", p.Tags)
	}
	n.expect("owner", "PATCH", productPath, fmt.Sprintf(`{"tags":[%q]}`, strings.Repeat("x", 256)), 400)
	n.expect("staff", "PATCH", productPath, `{"tags":["Mine"]}`, 403)

	// A price can be removed only once nothing is for sale at it; then the product leaves the list.
	path := fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", product.ID, n.small.ID)
	n.expect("owner", "DELETE", path, "", 409)
	n.expect("owner", "POST", "/api/orgs/{org}/plants/unmark-for-sale", `{"barcodes":["IT-000001","IT-000002","IT-000003","IT-000004","IT-000005"]}`, 200)
	n.expect("owner", "DELETE", path, "", 204)
	if got := n.products(); len(got) != 0 {
		t.Errorf("products = %+v, want none left", got)
	}

	var rivals []productResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/products", "", 200).Body.Bytes(), &rivals)
	if len(rivals) != 0 {
		t.Errorf("the other nursery sees %+v", rivals)
	}
}

func TestStockFollowsThePlants(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)

	// A for-sale plant moved to a bigger pot moves to that pot's stock, getting a SKU there.
	if got, _ := n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000004"]}`, n.medium.ID), 200); len(got.NewVariants) != 1 || got.NewVariants[0].SKU != "ABDS-MC" {
		t.Errorf("repot created %+v, want the Medium Cup SKU ABDS-MC", got.NewVariants)
	}
	if s := stock(n.product(dragonScale)); s["Small Cup"] != 4 || s["Medium Cup"] != 1 {
		t.Errorf("stock = %v, want 4 Small Cup and 1 Medium Cup", s)
	}
}
