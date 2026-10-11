package inventory

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDashboard(t *testing.T) {
	n := newRepotNursery(t) // 5 Dragon Scales (IT-000001 to 5) in the Tissue Culture Cup
	dashboard := func() dashboardResponse {
		var d dashboardResponse
		_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/dashboard", "", 200).Body.Bytes(), &d)
		return d
	}

	if d := dashboard(); d.Plants.Growing != 5 || d.ReadyToMark != 0 || d.Last30Days.Received != 5 {
		t.Errorf("at the start: %+v, want 5 growing, none ready (all in the unsellable cup)", d)
	}

	// 4 move to the Small Cup, 1 is discarded; 2 of the 4 go on sale (their SKU has no price yet).
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001","IT-000002","IT-000003","IT-000004"]}`, n.small.ID), 200)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000005"]}`, 200)
	n.markForSale(`{"barcodes":["IT-000001","IT-000002"]}`, 200)

	d := dashboard()
	if d.Plants != (plantCountsJSON{Growing: 2, ForSale: 2, Discarded: 1}) || d.Last30Days.Discarded != 1 {
		t.Errorf("plants = %+v (discarded in 30 days: %d), want 2 growing, 2 for sale, 1 discarded", d.Plants, d.Last30Days.Discarded)
	}
	if d.ReadyToMark != 2 || d.NeedsPrice != 2 || d.ForSaleValueCents != 0 {
		t.Errorf("ready %d, needs price %d, value %d; want 2 ready, 2 needing a price, $0 (unpriced)", d.ReadyToMark, d.NeedsPrice, d.ForSaleValueCents)
	}

	// Priced at $45: nothing needs a price, and the for-sale value counts them.
	n.setVariant("owner", n.product(dragonScale).ID, n.small.ID, `{"priceCents":4500}`, 200)
	if d := dashboard(); d.NeedsPrice != 0 || d.ForSaleValueCents != 9000 {
		t.Errorf("after pricing: needs price %d, value %d; want 0 and 9000", d.NeedsPrice, d.ForSaleValueCents)
	}

	pipeline := map[string]potSizePipelineJSON{}
	for _, ps := range dashboard().PotSizes {
		pipeline[ps.Name] = ps
	}
	if pipeline["Tissue Culture Cup"].Growing != 0 || pipeline["Small Cup"].Growing != 2 || pipeline["Small Cup"].ForSale != 2 {
		t.Errorf("pipeline = %+v, want Small Cup: 2 growing + 2 for sale", pipeline)
	}

	var rival dashboardResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/dashboard", "", 200).Body.Bytes(), &rival)
	if rival.Plants != (plantCountsJSON{}) || len(rival.PotSizes) != 0 {
		t.Errorf("the other nursery's dashboard = %+v, want it empty", rival)
	}
}
