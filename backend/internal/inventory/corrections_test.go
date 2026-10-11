package inventory

import (
	"encoding/json"
	"fmt"
	"testing"
)

func (n *repotNursery) correct(as, body string, wantStatus int) correctResponse {
	n.t.Helper()
	var result correctResponse
	_ = json.Unmarshal(n.expect(as, "POST", "/api/orgs/{org}/plants/correct", body, wantStatus).Body.Bytes(), &result)
	return result
}

func (n *repotNursery) aureaID() int64 {
	n.t.Helper()
	var id int64
	if err := n.db.QueryRow(n.t.Context(), "SELECT id FROM variegations WHERE name = 'Aurea' AND organization_id IS NULL").Scan(&id); err != nil {
		n.t.Fatal(err)
	}
	return id
}

// TestPlantLosesItsVariegation: a for-sale Aurea plant turns out plain. It moves to the plain
// product (whose Small Cup SKU is created), and its history says what changed.
func TestPlantLosesItsVariegation(t *testing.T) {
	n := newRepotNursery(t) // IT-000001 to 5: plain Dragon Scale
	aurea := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"variegationId":%d,"potSizeId":%d,"quantity":3,"receivedOn":"2026-10-11"}`,
		n.plantNameID(dragonScale), n.aureaID(), n.small.ID), 201) // IT-000006 to 8
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, aurea.Batch.ID), 200)

	got := n.correct("staff", `{"barcodes":["IT-000006"],"variegationId":null}`, 200)
	if got.Corrected != 1 || len(got.NewVariants) != 1 || got.NewVariants[0].SKU != "ABDS-SC" {
		t.Errorf("correction = %+v, want 1 corrected and the plain Small Cup SKU created", got)
	}
	if s := stock(n.product(dragonScale + " Aurea")); s["Small Cup"] != 2 {
		t.Errorf("Aurea stock = %v, want 2 left", s)
	}
	if s := stock(n.product(dragonScale)); s["Small Cup"] != 1 {
		t.Errorf("plain stock = %v, want the corrected plant", s)
	}

	plant := n.plant("IT-000006")
	last := plant.History[len(plant.History)-1]
	if plant.Variegation != nil || last.Type != "name_corrected" ||
		string(last.Details) != fmt.Sprintf(`{"to":"%s","from":"%s Aurea"}`, dragonScale, dragonScale) {
		t.Errorf("plant variegation %v, last event %s %s; want none, and a correction from Aurea", plant.Variegation, last.Type, last.Details)
	}
	if again := n.correct("staff", `{"barcodes":["IT-000006"],"variegationId":null}`, 200); again.Corrected != 0 || again.Unchanged != 1 {
		t.Errorf("correcting again = %+v, want it already right", again)
	}
}

// TestCorrectAWholeBatch: the lab sent Polly, not Dragon Scale. The plants and the batch record
// are fixed; a discarded plant is left as it was.
func TestCorrectAWholeBatch(t *testing.T) {
	n := newRepotNursery(t)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000005"]}`, 200)
	polly := n.plantNameID("Alocasia 'Polly'")

	if got := n.correct("staff", fmt.Sprintf(`{"batchId":%d,"plantNameId":%d}`, n.batch.ID, polly), 200); got.Corrected != 4 || got.Unchanged != 0 {
		t.Errorf("correcting the batch = %+v, want the 4 living plants corrected", got)
	}
	var batch batchWithPlantsResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", fmt.Sprintf("/api/orgs/{org}/batches/%d", n.batch.ID), "", 200).Body.Bytes(), &batch)
	names := map[string]string{}
	for _, p := range batch.Plants {
		names[p.Barcode] = p.PlantName
	}
	if batch.Batch.PlantName != "Alocasia 'Polly'" || names["IT-000001"] != "Alocasia 'Polly'" || names["IT-000005"] != dragonScale {
		t.Errorf("batch says %q; IT-000001 %q, discarded IT-000005 %q; want Polly, Polly, and Dragon Scale left alone", batch.Batch.PlantName, names["IT-000001"], names["IT-000005"])
	}
}

func TestCorrectionRules(t *testing.T) {
	n := newRepotNursery(t)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000005"]}`, 200)

	n.correct("staff", `{"barcodes":["IT-000001"]}`, 400)                                                            // nothing to change
	n.correct("staff", fmt.Sprintf(`{"batchId":%d,"barcodes":["IT-000001"],"variegationId":null}`, n.batch.ID), 400) // both
	n.correct("staff", fmt.Sprintf(`{"barcodes":["IT-000001"],"plantNameId":%d}`, n.plantNameID("Araceae")), 400)    // a family
	n.correct("staff", `{"barcodes":["IT-000001"],"variegationId":999999}`, 400)                                     // unknown variegation
	n.correct("staff", `{"barcodes":["IT-000005"],"variegationId":null}`, 400)                                       // discarded
	n.correct("viewer", `{"barcodes":["IT-000001"],"variegationId":null}`, 403)                                      // inventory.edit
	n.correct("rival", fmt.Sprintf(`{"batchId":%d,"variegationId":null}`, n.batch.ID), 404)                          // not theirs
}

func TestPlantNotes(t *testing.T) {
	n := newRepotNursery(t)
	path := "/api/orgs/{org}/plants/by-barcode/IT-000001"

	var plant plantWithHistoryResponse
	_ = json.Unmarshal(n.expect("staff", "PATCH", path, `{"notes":" Mealybugs, treated Oct 11 "}`, 200).Body.Bytes(), &plant)
	if plant.Notes == nil || *plant.Notes != "Mealybugs, treated Oct 11" {
		t.Errorf("notes = %v, want them trimmed", plant.Notes)
	}
	_ = json.Unmarshal(n.expect("staff", "PATCH", path, `{"notes":""}`, 200).Body.Bytes(), &plant)
	if plant.Notes != nil {
		t.Errorf("notes = %q, want them cleared", *plant.Notes)
	}
	n.expect("staff", "PATCH", path, `{}`, 400)
	n.expect("viewer", "PATCH", path, `{"notes":"x"}`, 403)
	n.expect("staff", "PATCH", "/api/orgs/{org}/plants/by-barcode/IT-999999", `{"notes":"x"}`, 404)
}

func TestEditBatch(t *testing.T) {
	n := newRepotNursery(t)
	path := fmt.Sprintf("/api/orgs/{org}/batches/%d", n.batch.ID)
	batch := func(as, body string, wantStatus int) batchResponse {
		t.Helper()
		var b batchWithPlantsResponse
		_ = json.Unmarshal(n.expect(as, "PATCH", path, body, wantStatus).Body.Bytes(), &b)
		return b.Batch
	}

	got := batch("staff", `{"source":" Lab Y ","notes":"Arrived damp","receivedOn":"2026-10-02"}`, 200)
	if *got.Source != "Lab Y" || *got.Notes != "Arrived damp" || got.ReceivedOn != "2026-10-02" {
		t.Errorf("batch = %+v, want the new source, notes, and date", got)
	}
	if got := batch("staff", `{"source":""}`, 200); got.Source != nil || *got.Notes != "Arrived damp" {
		t.Errorf("batch = %+v, want the source cleared and the notes kept", got)
	}

	batch("staff", `{"unitCostCents":900}`, 403) // cost is for financials.view
	if got := batch("owner", `{"unitCostCents":900}`, 200); got.UnitCostCents == nil || *got.UnitCostCents != 900 {
		t.Errorf("cost = %v, want 900", got.UnitCostCents)
	}
	if got := batch("owner", `{"unitCostCents":null}`, 200); got.UnitCostCents != nil {
		t.Errorf("cost = %v, want it cleared", *got.UnitCostCents)
	}
	if got := batch("owner", `{"notes":"Still damp"}`, 200); got.ReceivedOn != "2026-10-02" || *got.Notes != "Still damp" {
		t.Errorf("batch = %+v, want the new notes and the date kept", got)
	}
	batch("staff", `{"receivedOn":"Oct 2"}`, 400)
	batch("viewer", `{"notes":"x"}`, 403)
	n.expect("rival", "PATCH", path, `{"notes":"x"}`, 404)
}
