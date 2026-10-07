package inventory

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDashboard(t *testing.T) {
	n := newRepotNursery(t) // 5 Dragon Scales (IT-000001 to 5) in the Tissue Culture Cup

	var empty dashboardResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/dashboard", "", 200).Body.Bytes(), &empty)
	if empty.Plants.Growing != 5 || empty.ReadyToMark != 0 || empty.NeedsPrice != 0 || empty.Last30Days.Received != 5 {
		t.Errorf("at the start: %+v, want 5 growing, all still in the unsellable cup", empty)
	}

	// 4 move to the Small Cup (priced $45), 1 is discarded; 2 of the 4 go on sale.
	product := n.createProduct("owner", fmt.Sprintf(`{"plantNameId":%d}`, n.plantNameID("Alocasia baginda 'Dragon Scale'")), 201)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001","IT-000002","IT-000003","IT-000004"]}`, n.small.ID), 200)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000005"]}`, 200)
	n.setPrice(product.ID, n.small.ID, `{"sku":"DS-S","priceCents":4500}`, 200)
	n.markForSale(`{"barcodes":["IT-000001","IT-000002"]}`, 200)
	// And one plant in the Medium Cup, which has no price.
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000004"]}`, n.medium.ID), 200)

	var d dashboardResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/dashboard", "", 200).Body.Bytes(), &d)
	want := plantCountsJSON{Growing: 2, ForSale: 2, Discarded: 1}
	if d.Plants != want || d.Last30Days.Discarded != 1 {
		t.Errorf("plants = %+v (discarded in 30 days: %d), want %+v and 1", d.Plants, d.Last30Days.Discarded, want)
	}
	if d.ForSaleValueCents != 9000 || d.ReadyToMark != 1 || d.NeedsPrice != 1 {
		t.Errorf("for sale $%.2f, ready to mark %d, needs price %d; want $90.00, 1 (IT-000003), 1 (IT-000004 in Medium)",
			float64(d.ForSaleValueCents)/100, d.ReadyToMark, d.NeedsPrice)
	}
	pipeline := map[string]potSizePipelineJSON{}
	for _, ps := range d.PotSizes {
		pipeline[ps.Name] = ps
	}
	if len(d.PotSizes) != 3 || d.PotSizes[0].Name != "Tissue Culture Cup" ||
		pipeline["Small Cup"].Growing != 1 || pipeline["Small Cup"].ForSale != 2 || pipeline["Medium Cup"].Growing != 1 {
		t.Errorf("pot sizes = %+v, want TC first; Small 1 growing + 2 for sale; Medium 1 growing", d.PotSizes)
	}

	var rival dashboardResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/dashboard", "", 200).Body.Bytes(), &rival)
	if rival.Plants != (plantCountsJSON{}) || len(rival.PotSizes) != 0 {
		t.Errorf("the other nursery's dashboard = %+v, want it empty", rival)
	}
}
