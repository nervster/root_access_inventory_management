package inventory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// repotNursery has pot sizes and one batch of 5 Dragon Scales (IT-000001 to IT-000005) in the
// Tissue Culture Cup.
type repotNursery struct {
	*nursery
	tc, small, medium potSizeResponse
	batch             batchResponse
}

func newRepotNursery(t *testing.T) *repotNursery {
	n := &repotNursery{nursery: newNursery(t)}
	n.tc = n.addPotSize("owner", `{"name":"Tissue Culture Cup","sellable":false}`)
	n.small = n.addPotSize("owner", `{"name":"Small Cup"}`)
	n.medium = n.addPotSize("owner", `{"name":"Medium Cup"}`)
	n.batch = n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":5,"receivedOn":"2026-10-01"}`,
		n.plantNameID("Alocasia baginda 'Dragon Scale'"), n.tc.ID), 201).Batch
	return n
}

func (n *repotNursery) repot(body string, wantStatus int) (result repotResponse, errorMessage string) {
	n.t.Helper()
	recorder := n.expect("staff", "POST", "/api/orgs/{org}/repot", body, wantStatus)
	if wantStatus == http.StatusOK {
		_ = json.Unmarshal(recorder.Body.Bytes(), &result)
	} else {
		var e struct{ Error string }
		_ = json.Unmarshal(recorder.Body.Bytes(), &e)
		errorMessage = e.Error
	}
	return result, errorMessage
}

func (n *repotNursery) plant(barcode string) plantWithHistoryResponse {
	n.t.Helper()
	var plant plantWithHistoryResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plants/by-barcode/"+barcode, "", 200).Body.Bytes(), &plant)
	return plant
}

func TestRepotBatchThenExceptions(t *testing.T) {
	n := newRepotNursery(t)

	// The whole batch moves up to the Small Cup.
	if got, _ := n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200); got != (repotResponse{Moved: 5}) {
		t.Errorf("repotting the batch: %+v, want 5 moved", got)
	}
	if got, _ := n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200); got != (repotResponse{Unchanged: 5}) {
		t.Errorf("repotting it again: %+v, want all 5 unchanged", got)
	}

	// Two plants grew faster: scanned (one twice, one in lower case) into the Medium Cup.
	if got, _ := n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000004","it-000005","IT-000005"]}`, n.medium.ID), 200); got != (repotResponse{Moved: 2}) {
		t.Errorf("repotting 2 plants: %+v, want 2 moved", got)
	}

	plant := n.plant("IT-000004")
	var steps []string
	for _, change := range plant.PotHistory {
		from := "start"
		if change.FromPotSize != nil {
			from = *change.FromPotSize
		}
		steps = append(steps, from+" → "+change.ToPotSize)
	}
	if plant.PotSize != "Medium Cup" || strings.Join(steps, ", ") != "start → Tissue Culture Cup, Tissue Culture Cup → Small Cup, Small Cup → Medium Cup" {
		t.Errorf("IT-000004 is in %s with history %v, want Medium Cup after TC → Small → Medium", plant.PotSize, steps)
	}
	if plant := n.plant("IT-000001"); plant.PotSize != "Small Cup" {
		t.Errorf("IT-000001 is in %s, want it left in the Small Cup", plant.PotSize)
	}
}

func TestDiscardPlants(t *testing.T) {
	n := newRepotNursery(t)

	var result struct{ Discarded int }
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000003"],"reason":" root rot "}`, 200).Body.Bytes(), &result)
	if result.Discarded != 1 {
		t.Errorf("discarded %d, want 1", result.Discarded)
	}
	plant := n.plant("IT-000003")
	if plant.Status != dbgen.PlantStatusDiscarded || plant.DiscardedAt == nil || *plant.DiscardReason != "root rot" {
		t.Errorf("IT-000003 = %+v, want discarded for root rot", plant.plantResponse)
	}

	// A batch repot skips it; listing it by barcode is refused, naming it.
	if got, _ := n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200); got != (repotResponse{Moved: 4}) {
		t.Errorf("repotting the batch: %+v, want the 4 living plants moved", got)
	}
	if _, message := n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000003","IT-999999"]}`, n.medium.ID), 400); !strings.Contains(message, "IT-999999") || !strings.Contains(message, "IT-000003") {
		t.Errorf("error = %q, want it to name the unknown IT-999999 and the discarded IT-000003", message)
	}
	if plant := n.plant("IT-000003"); plant.PotSize != "Tissue Culture Cup" {
		t.Errorf("the discarded plant moved to %s, want it left where it was", plant.PotSize)
	}
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000003"]}`, 400) // already discarded
}

func TestRepotForSalePlantIntoUnsellablePot(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.small.ID), 200)
	if _, err := n.db.Exec(t.Context(), "UPDATE plants SET status = 'for_sale' WHERE barcode = 'IT-000001' AND organization_id = $1", n.org.ID); err != nil {
		t.Fatal(err)
	}

	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.tc.ID), 200)
	if plant := n.plant("IT-000001"); plant.Status != dbgen.PlantStatusGrowing {
		t.Errorf("status = %s after moving into the Tissue Culture Cup, want growing (that cup can't be sold)", plant.Status)
	}
}

func TestRepotValidation(t *testing.T) {
	n := newRepotNursery(t)
	var rivalPot int64
	if err := n.db.QueryRow(t.Context(), "INSERT INTO pot_sizes (organization_id, name) VALUES ($1, 'Rival Cup') RETURNING id", n.other.ID).Scan(&rivalPot); err != nil {
		t.Fatal(err)
	}

	n.repot(fmt.Sprintf(`{"potSizeId":%d}`, n.small.ID), 400)                                                   // neither batch nor barcodes
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d,"barcodes":["IT-000001"]}`, n.small.ID, n.batch.ID), 400) // both
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, rivalPot, n.batch.ID), 400)                            // another nursery's pot
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":999999}`, n.small.ID), 404)                                  // unknown batch
	n.expect("viewer", "POST", "/api/orgs/{org}/repot", fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 403)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":[]}`, 400)

	// The rival nursery can't move this nursery's plants: to it, the barcodes don't exist.
	var rivalSmall int64
	if err := n.db.QueryRow(t.Context(), "INSERT INTO pot_sizes (organization_id, name) VALUES ($1, 'Rival Small') RETURNING id", n.other.ID).Scan(&rivalSmall); err != nil {
		t.Fatal(err)
	}
	n.expect("rival", "POST", "/api/orgs/{org}/repot", fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, rivalSmall), 400)
	if plant := n.plant("IT-000001"); plant.PotSize != "Tissue Culture Cup" {
		t.Errorf("IT-000001 is in %s, want it untouched by the other nursery", plant.PotSize)
	}
}
