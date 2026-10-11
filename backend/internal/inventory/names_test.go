package inventory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func (n *repotNursery) addName(as, body string, wantStatus int) (plantNameResponse, string) {
	n.t.Helper()
	recorder := n.expect(as, "POST", "/api/orgs/{org}/plant-names", body, wantStatus)
	var name plantNameResponse
	var e struct{ Error string }
	_ = json.Unmarshal(recorder.Body.Bytes(), &name)
	_ = json.Unmarshal(recorder.Body.Bytes(), &e)
	return name, e.Error
}

func (n *repotNursery) searchNames(as, query string) []string {
	n.t.Helper()
	var names []plantNameResponse
	_ = json.Unmarshal(n.expect(as, "GET", "/api/orgs/{org}/plant-names?"+query, "", 200).Body.Bytes(), &names)
	var full []string
	for _, name := range names {
		full = append(full, name.FullName)
	}
	return full
}

func TestAddOwnPlantNames(t *testing.T) {
	n := newRepotNursery(t)
	family := n.plantNameID("Asparagaceae")

	genus, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"genus","name":" Dracaena "}`, family), http.StatusCreated)
	species, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"species","name":"trifasciata","notes":"snake plant"}`, genus.ID), http.StatusCreated)
	cultivar, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"cultivar","name":"'Moonshine'"}`, species.ID), http.StatusCreated)
	hybrid, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"cultivar","name":"Night Owl"}`, genus.ID), http.StatusCreated)

	got := []string{genus.FullName, species.FullName, cultivar.FullName, hybrid.FullName}
	want := []string{"Dracaena", "Dracaena trifasciata", "Dracaena trifasciata 'Moonshine'", "Dracaena 'Night Owl'"}
	if fmt.Sprint(got) != fmt.Sprint(want) || cultivar.Shared {
		t.Errorf("added %v (shared %v), want %v, the nursery's own", got, cultivar.Shared, want)
	}

	// Found by search, and usable for a batch; the other nursery doesn't see them.
	if found := n.searchNames("viewer", "search=moonshine"); len(found) != 1 || found[0] != cultivar.FullName {
		t.Errorf("searching moonshine found %v, want the new cultivar", found)
	}
	n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-11"}`, cultivar.ID, n.small.ID), 201)
	if found := n.searchNames("rival", "search=moonshine"); len(found) != 0 {
		t.Errorf("the other nursery found %v", found)
	}
	n.expect("rival", "POST", "/api/orgs/{org}/batches",
		fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-11"}`, cultivar.ID, n.small.ID), 400) // not found for them
}

func TestPlantNameRules(t *testing.T) {
	n := newRepotNursery(t)
	araceae := n.plantNameID("Araceae")
	alocasia := n.plantNameID("Alocasia")

	// Already in the shared list (any capitalization): refused, naming it.
	if _, message := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"genus","name":"alocasia"}`, araceae), 409); !strings.Contains(message, "Alocasia") {
		t.Errorf("error = %q, want it to name the existing Alocasia", message)
	}
	// In the wrong place.
	n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"cultivar","name":"X"}`, araceae), 400) // a cultivar under a family
	n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"genus","name":"X"}`, alocasia), 400)   // a genus under a genus
	n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"family","name":"X"}`, araceae), 400)   // a family under anything
	n.addName("staff", `{"rank":"genus","name":"X"}`, 400)                                        // a genus with no family
	n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"tribe","name":"X"}`, araceae), 400)    // not a rank
	n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"genus","name":"  "}`, araceae), 400)   //
	n.addName("viewer", fmt.Sprintf(`{"parentId":%d,"rank":"genus","name":"Y"}`, araceae), 403)   // inventory.edit
	n.addName("staff", `{"rank":"family","name":"Orchidaceae Nova"}`, 201)                        // a new family is fine

	// Families show in search only when asked for (when choosing where a genus goes).
	if found := n.searchNames("viewer", "search=araceae"); len(found) != 0 {
		t.Errorf("plain search found %v, want no families", found)
	}
	if found := n.searchNames("viewer", "search=araceae&includeFamilies=true"); len(found) != 1 || found[0] != "Araceae" {
		t.Errorf("search with families found %v, want Araceae", found)
	}
}

func TestDeleteOwnNames(t *testing.T) {
	n := newRepotNursery(t)
	unused, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"cultivar","name":"Unused"}`, n.plantNameID("Alocasia")), 201)
	used, _ := n.addName("staff", fmt.Sprintf(`{"parentId":%d,"rank":"cultivar","name":"Used"}`, n.plantNameID("Alocasia")), 201)
	n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-11"}`, used.ID, n.small.ID), 201)

	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/plant-names/%d", used.ID), "", 409)
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/plant-names/%d", unused.ID), "", 204)
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/plant-names/%d", n.plantNameID("Alocasia")), "", 404) // shared
	n.expect("rival", "DELETE", fmt.Sprintf("/api/orgs/{org}/plant-names/%d", used.ID), "", 404)                   // not theirs
}

func TestOwnVariegations(t *testing.T) {
	n := newRepotNursery(t)
	var sport variegationResponse
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/variegations", `{"name":" Sport ","notes":"a one-off mutation"}`, 201).Body.Bytes(), &sport)
	if sport.Name != "Sport" || sport.Shared {
		t.Errorf("variegation = %+v, want the nursery's own Sport", sport)
	}
	n.expect("staff", "POST", "/api/orgs/{org}/variegations", `{"name":"albo"}`, 409)  // the shared Albo
	n.expect("staff", "POST", "/api/orgs/{org}/variegations", `{"name":"sport"}`, 409) // already theirs
	n.expect("viewer", "POST", "/api/orgs/{org}/variegations", `{"name":"Mine"}`, 403)

	var rivals []variegationResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/variegations", "", 200).Body.Bytes(), &rivals)
	for _, v := range rivals {
		if v.Name == "Sport" {
			t.Error("the other nursery sees this nursery's Sport")
		}
	}
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/variegations/%d", sport.ID), "", 204)
}
