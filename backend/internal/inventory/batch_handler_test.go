package inventory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// plantNameID returns the ID of a shared plant name by its full name.
func (n *nursery) plantNameID(fullName string) int64 {
	n.t.Helper()
	var id int64
	if err := n.db.QueryRow(n.t.Context(), "SELECT id FROM plant_names WHERE full_name = $1 AND organization_id IS NULL", fullName).Scan(&id); err != nil {
		n.t.Fatalf("plant name %q: %v", fullName, err)
	}
	return id
}

func (n *nursery) receiveBatch(as, body string, wantStatus int) batchWithPlantsResponse {
	n.t.Helper()
	var batch batchWithPlantsResponse
	recorder := n.expect(as, "POST", "/api/orgs/{org}/batches", body, wantStatus)
	if wantStatus == http.StatusCreated {
		if err := json.Unmarshal(recorder.Body.Bytes(), &batch); err != nil {
			n.t.Fatal(err)
		}
	}
	return batch
}

func TestReceiveBatch(t *testing.T) {
	n := newNursery(t)
	cup := n.addPotSize("owner", `{"name":"Tissue Culture Cup","sellable":false}`)
	dragonScale := n.plantNameID("Alocasia baginda 'Dragon Scale'")

	first := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":3,"receivedOn":"2026-10-01","source":" Lab X "}`,
		dragonScale, cup.ID), 201)
	if first.Batch.Code != "B-0001" || first.Batch.ReceivedOn != "2026-10-01" || *first.Batch.Source != "Lab X" || first.Batch.PlantCounts.Growing != 3 {
		t.Errorf("batch = %+v, want B-0001 received 2026-10-01 from Lab X with 3 growing", first.Batch)
	}
	// The nursery's slug is "inventory-test", so its barcodes start with IT.
	var barcodes []string
	for _, p := range first.Plants {
		barcodes = append(barcodes, p.Barcode)
		if p.PotSize != "Tissue Culture Cup" || p.Status != dbgen.PlantStatusGrowing || p.PlantName != "Alocasia baginda 'Dragon Scale'" {
			t.Errorf("plant = %+v, want a growing Dragon Scale in the Tissue Culture Cup", p)
		}
	}
	if strings.Join(barcodes, ",") != "IT-000001,IT-000002,IT-000003" {
		t.Errorf("barcodes = %v, want IT-000001 to IT-000003", barcodes)
	}

	// The next batch continues the numbering.
	second := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":2,"receivedOn":"2026-10-02"}`, dragonScale, cup.ID), 201)
	if second.Batch.Code != "B-0002" || second.Plants[0].Barcode != "IT-000004" || second.Plants[1].Barcode != "IT-000005" {
		t.Errorf("second batch = %s with %s…, want B-0002 starting at IT-000004", second.Batch.Code, second.Plants[0].Barcode)
	}

	var batches []batchResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/batches", "", 200).Body.Bytes(), &batches)
	if len(batches) != 2 || batches[0].Code != "B-0002" {
		t.Errorf("batches = %+v, want B-0002 then B-0001 (newest first)", batches)
	}
}

func TestBatchCostIsForFinancialsOnly(t *testing.T) {
	n := newNursery(t)
	cup := n.addPotSize("owner", `{"name":"Tissue Culture Cup","sellable":false}`)
	body := fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-01","unitCostCents":1200}`,
		n.plantNameID("Monstera deliciosa 'Thai Constellation'"), cup.ID)

	n.receiveBatch("staff", body, 403) // Staff can receive batches, but not set what they cost
	batch := n.receiveBatch("owner", body, 201)
	if batch.Batch.UnitCostCents == nil || *batch.Batch.UnitCostCents != 1200 {
		t.Errorf("owner sees cost %v, want 1200", batch.Batch.UnitCostCents)
	}

	path := fmt.Sprintf("/api/orgs/{org}/batches/%d", batch.Batch.ID)
	if body := n.expect("staff", "GET", path, "", 200).Body.String(); strings.Contains(body, "unitCostCents") {
		t.Errorf("staff response contains the cost: %s", body)
	}
}

func TestReceiveBatchValidation(t *testing.T) {
	n := newNursery(t)
	cup := n.addPotSize("owner", `{"name":"Tissue Culture Cup","sellable":false}`)
	name := n.plantNameID("Alocasia baginda 'Dragon Scale'")
	family := n.plantNameID("Araceae")

	var rivalPot int64
	if err := n.db.QueryRow(t.Context(), "INSERT INTO pot_sizes (organization_id, name) VALUES ($1, 'Rival Cup') RETURNING id", n.other.ID).Scan(&rivalPot); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, body string
		want       int
	}{
		{"no plants", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":0,"receivedOn":"2026-10-01"}`, name, cup.ID), 400},
		{"too many plants", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":501,"receivedOn":"2026-10-01"}`, name, cup.ID), 400},
		{"bad date", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"10/01/2026"}`, name, cup.ID), 400},
		{"a family, not a plant", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-01"}`, family, cup.ID), 400},
		{"unknown variegation", fmt.Sprintf(`{"plantNameId":%d,"variegationId":999999,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-01"}`, name, cup.ID), 400},
		{"another nursery's pot size", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-01"}`, name, rivalPot), 400},
		{"viewer", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-01"}`, name, cup.ID), 403},
	} {
		t.Run(tt.name, func(t *testing.T) {
			as := "staff"
			if tt.name == "viewer" {
				as = "viewer"
			}
			n.t = t
			n.receiveBatch(as, tt.body, tt.want)
		})
	}
}

func TestFindPlants(t *testing.T) {
	n := newNursery(t)
	cup := n.addPotSize("owner", `{"name":"Tissue Culture Cup","sellable":false}`)

	var aurea int64
	var variegations []variegationResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/variegations", "", 200).Body.Bytes(), &variegations)
	for _, v := range variegations {
		if v.Name == "Aurea" && v.Shared {
			aurea = v.ID
		}
	}
	if aurea == 0 {
		t.Fatalf("variegations = %+v, want the shared Aurea", variegations)
	}

	var names []plantNameResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plant-names?search=dragon", "", 200).Body.Bytes(), &names)
	if len(names) == 0 || names[0].FullName != "Alocasia baginda 'Dragon Scale'" {
		t.Fatalf("searching dragon found %+v, want Dragon Scale first", names)
	}

	batch := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"variegationId":%d,"potSizeId":%d,"quantity":2,"receivedOn":"2026-10-01"}`,
		names[0].ID, aurea, cup.ID), 201)

	var plants []plantResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", fmt.Sprintf("/api/orgs/{org}/plants?batchId=%d&status=growing", batch.Batch.ID), "", 200).Body.Bytes(), &plants)
	if len(plants) != 2 || *plants[0].Variegation != "Aurea" {
		t.Errorf("plants = %+v, want the 2 Aurea plants", plants)
	}
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plants?status=sold", "", 200).Body.Bytes(), &plants)
	if len(plants) != 0 {
		t.Errorf("sold plants = %+v, want none", plants)
	}
	n.expect("viewer", "GET", "/api/orgs/{org}/plants?status=dead", "", 400)
	n.expect("viewer", "GET", "/api/orgs/{org}/plants?batchId=abc", "", 400)

	// Scanning a label: lower-case is fine, and the pot history starts in the first pot.
	var scanned plantWithHistoryResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plants/by-barcode/it-000002", "", 200).Body.Bytes(), &scanned)
	if scanned.Barcode != "IT-000002" || len(scanned.PotHistory) != 1 || scanned.PotHistory[0].FromPotSize != nil ||
		scanned.PotHistory[0].ToPotSize != "Tissue Culture Cup" || *scanned.PotHistory[0].ChangedBy != "staff@inventory.example.com" {
		t.Errorf("scanned = %+v, want IT-000002 with its first pot recorded by staff", scanned)
	}
	n.expect("viewer", "GET", "/api/orgs/{org}/plants/by-barcode/IT-999999", "", 404)
	n.expect("rival", "GET", "/api/orgs/{org}/plants/by-barcode/IT-000002", "", 404) // another nursery can't find it
}
