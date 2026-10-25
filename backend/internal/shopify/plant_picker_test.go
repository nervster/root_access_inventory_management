package shopify

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// photographPlant adds a photo of the plant, taken at takenAt, and returns its storage key.
func (s *shopTest) photographPlant(barcode, name string, takenAt time.Time) string {
	s.t.Helper()
	key := fmt.Sprintf("orgs/%d/plants/%s/%s.jpg", s.org.ID, barcode, name)
	_, err := s.db.Exec(s.t.Context(), `
		INSERT INTO plant_photos (organization_id, plant_id, storage_key, taken_at)
		SELECT organization_id, id, $2, $3 FROM plants WHERE barcode = $1`, barcode, key, takenAt)
	if err != nil {
		s.t.Fatal(err)
	}
	return key
}

func (s *shopTest) plantID(barcode string) int64 {
	s.t.Helper()
	var id int64
	if err := s.db.QueryRow(s.t.Context(), "SELECT id FROM plants WHERE barcode = $1", barcode).Scan(&id); err != nil {
		s.t.Fatal(err)
	}
	return id
}

// storeVariant is the number the theme knows the product's SKU in the pot by.
func (s *shopTest) storeVariant(pot string) string {
	s.t.Helper()
	for _, v := range s.onlyProduct().Variants {
		if v.PotSize == pot {
			return v.ID[strings.LastIndex(v.ID, "/")+1:]
		}
	}
	s.t.Fatalf("no %s on the store", pot)
	return ""
}

func TestPlantPicker(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Dragon Scale", map[string]*int{"Small Cup": cents(4500), "Medium Cup": cents(9000)})
	s.sync(product)
	small := s.storeVariant("Small Cup")

	// Choosable: for sale, with a photo, in this pot. The first has two photos: the latest one is shown.
	plants := s.addPlants(product, "Small Cup", "for_sale", 3)
	a, b, noPhoto := plants[0], plants[1], plants[2]
	s.removePhotos(noPhoto)
	s.photographPlant(a, "old", time.Now().Add(-48*time.Hour))
	latestA := s.photographPlant(a, "new", time.Now().Add(-time.Hour))
	s.photographPlant(b, "only", time.Now().Add(-time.Hour))
	growing := s.addPlants(product, "Small Cup", "growing", 1)[0]
	s.photographPlant(growing, "growing", time.Now())
	medium := s.addPlants(product, "Medium Cup", "for_sale", 1)[0]
	s.photographPlant(medium, "medium", time.Now())
	idA, idB, idNoPhoto := s.plantID(a), s.plantID(b), s.plantID(noPhoto)

	list := func(cart string) pickablePlantsResponse {
		t.Helper()
		recorder := s.proxyGet("/plants", testShop, "test-secret", url.Values{"variant": {small}, "cartToken": {cart}})
		if recorder.Code != 200 {
			t.Fatalf("GET plants for %s: %d (%s)", cart, recorder.Code, recorder.Body)
		}
		var got pickablePlantsResponse
		_ = json.Unmarshal(recorder.Body.Bytes(), &got)
		return got
	}
	ids := func(got pickablePlantsResponse) []int64 {
		var ids []int64
		for _, p := range got.Plants {
			ids = append(ids, p.ID)
		}
		return ids
	}
	post := func(path, cart string, plants []int64, want int) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"cartToken": cart, "plants": plants})
		recorder := s.proxyRequest("POST", path, testShop, "test-secret", nil, string(body))
		if recorder.Code != want {
			t.Fatalf("POST %s %s: %d (%s), want %d", path, body, recorder.Code, recorder.Body, want)
		}
		return recorder
	}
	type failure struct {
		Error, Message string
		Plants         []int64
	}
	failed := func(recorder *httptest.ResponseRecorder) failure {
		var got failure
		_ = json.Unmarshal(recorder.Body.Bytes(), &got)
		return got
	}

	got := list("c1-a")
	if !slices.Equal(ids(got), []int64{idA, idB}) || got.Plants[0].Photo != "https://photos.example/"+latestA+"?signed" || got.Plants[0].Held {
		t.Fatalf("plants = %+v, want the two for-sale plants with photos, oldest first, the first with its latest photo", got)
	}

	// Hold: 201 for 30 minutes; the plant leaves other carts' lists but stays in its own, marked held.
	var hold plantHoldResponse
	_ = json.Unmarshal(post("/plant-holds", "c1-a", []int64{idA}, 201).Body.Bytes(), &hold)
	if !slices.Equal(hold.Plants, []int64{idA}) || time.Until(hold.ExpiresAt) < 29*time.Minute {
		t.Errorf("hold = %+v, want plant %d held for 30 minutes", hold, idA)
	}
	if !slices.Equal(ids(list("c1-b")), []int64{idB}) {
		t.Errorf("another cart's list = %v, want only %d", ids(list("c1-b")), idB)
	}
	if own := list("c1-a"); !slices.Equal(ids(own), []int64{idA, idB}) || !own.Plants[0].Held || own.Plants[1].Held {
		t.Errorf("own list = %+v, want both, the held one marked", own)
	}

	// All or none: a cart asking for a held plant and a free one gets neither, and learns which was taken.
	if f := failed(post("/plant-holds", "c1-b", []int64{idA, idB}, 409)); f.Error != "taken" || f.Message == "" || !slices.Equal(f.Plants, []int64{idA}) {
		t.Errorf("failure = %+v, want taken listing %d", f, idA)
	}
	if !slices.Equal(ids(list("c1-c")), []int64{idB}) {
		t.Error("after a failed hold, want the free plant still free")
	}
	// Plants that can't be chosen at all: no photo, not for sale, or unknown.
	if f := failed(post("/plant-holds", "c1-b", []int64{idNoPhoto, s.plantID(growing), 999999}, 409)); len(f.Plants) != 3 {
		t.Errorf("failure = %+v, want all three listed", f)
	}

	// Refresh keeps it; once it runs out, another cart can take it, and the refresh fails.
	post("/plant-holds/refresh", "c1-a", []int64{idA}, 200)
	if _, err := s.db.Exec(t.Context(), "UPDATE plant_holds SET expires_at = now() - interval '1 minute' WHERE cart_token = 'c1-a'"); err != nil {
		t.Fatal(err)
	}
	post("/plant-holds", "c1-b", []int64{idA}, 201)
	if f := failed(post("/plant-holds/refresh", "c1-a", []int64{idA}, 409)); !slices.Equal(f.Plants, []int64{idA}) {
		t.Errorf("refresh after another cart took it = %+v, want taken", f)
	}
	// Released, it's free again, and the first cart takes it back.
	post("/plant-holds/release", "c1-b", []int64{idA}, 204)
	post("/plant-holds/release", "c1-b", []int64{idA}, 204) // already gone: fine
	post("/plant-holds/refresh", "c1-a", []int64{idA}, 200)

	// One visitor holds at most 20 plants across their carts (c1-a holds one).
	more := s.addPlants(product, "Small Cup", "for_sale", 20)
	var moreIDs []int64
	for _, barcode := range more {
		s.photographPlant(barcode, "p", time.Now())
		moreIDs = append(moreIDs, s.plantID(barcode))
	}
	post("/plant-holds", "c1-d", moreIDs[:19], 201)
	if f := failed(post("/plant-holds", "c1-e", moreIDs[19:], 429)); f.Error != "too_many_holds" {
		t.Errorf("21st plant = %+v, want too_many_holds", f)
	}
	post("/plant-holds/refresh", "c1-d", moreIDs[:19], 200) // holding the same plants again isn't more

	// Bad requests and unknown variants.
	post("/plant-holds", "c1-a", nil, 400)
	post("/plant-holds", "", []int64{idB}, 400)
	post("/plant-holds", "c1-a", append(moreIDs, idB), 400) // 21 at once
	if code := s.proxyGet("/plants", testShop, "test-secret", url.Values{"variant": {"abc"}}).Code; code != 400 {
		t.Errorf("GET plants?variant=abc: %d, want 400", code)
	}
	unknown := s.proxyGet("/plants", testShop, "test-secret", url.Values{"variant": {strconv.Itoa(987654)}})
	if f := failed(unknown); unknown.Code != 404 || f.Error != "not_found" {
		t.Errorf("unknown variant: %d %+v, want 404 not_found", unknown.Code, f)
	}
	if code := s.proxyGet("/plants", testShop, "wrong-secret", url.Values{"variant": {small}}).Code; code != 401 {
		t.Errorf("unsigned: %d, want 401", code)
	}
}

// placeOrderForPlants adds an order with one Small Cup line per chosen plant, each with its _plant property, as
// the theme adds them (a plant value of "" leaves the property off).
func (s *shopTest) placeOrderForPlants(name string, plants ...string) {
	s.t.Helper()
	order := s.placeOrder(name, map[string]int{"Small Cup": 1})
	template := order["lineItems"].(map[string]any)["nodes"].([]map[string]any)[0]
	var lines []map[string]any
	for _, plant := range plants {
		line := map[string]any{}
		for k, v := range template {
			line[k] = v
		}
		line["id"] = s.fake.id("LineItem")
		if plant != "" {
			line["customAttributes"] = []map[string]string{{"key": "_plant", "value": plant}}
		}
		lines = append(lines, line)
	}
	order["lineItems"] = map[string]any{"nodes": lines}
}

func TestOrderHoldsChosenPlants(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Chosen", map[string]*int{"Small Cup": cents(4500)})
	plants := s.addPlants(product, "Small Cup", "for_sale", 4)
	oldest, chosen, gone := plants[0], plants[1], plants[2]
	s.sync(product)

	// The shopper's cart held the plant it chose; another order took the plant they'd have got without choosing.
	body := fmt.Sprintf(`{"cartToken":"c1-a","plants":[%d]}`, s.plantID(chosen))
	if code := s.proxyRequest("POST", "/plant-holds", testShop, "test-secret", nil, body).Code; code != 201 {
		t.Fatalf("hold: %d", code)
	}
	s.setPlant(gone, "status = 'discarded'")

	// One line chose a plant, one chose a plant that's gone, one chose a plant that doesn't exist (a bad theme
	// value), and one chose nothing.
	s.placeOrderForPlants("#1001", strconv.FormatInt(s.plantID(chosen), 10), strconv.FormatInt(s.plantID(gone), 10), "999999", "")
	s.importOrders()

	orders := s.ordersWith("open")
	if len(orders) != 1 || len(orders[0].Items) != 4 {
		t.Fatalf("open orders = %+v, want #1001 with 4 items", orders)
	}
	items := orders[0].Items
	if !slices.Equal(items[0].HeldBarcodes, []string{chosen}) || items[0].ChosenBarcode == nil || *items[0].ChosenBarcode != chosen {
		t.Errorf("chosen line = %+v, want exactly %s held and named", items[0], chosen)
	}
	if len(items[1].HeldBarcodes) != 0 || items[1].ChosenBarcode == nil || *items[1].ChosenBarcode != gone {
		t.Errorf("gone line = %+v, want nothing held (no swap) and %s named for staff", items[1], gone)
	}
	if len(items[2].HeldBarcodes) != 0 || items[2].ChosenBarcode != nil {
		t.Errorf("unknown plant line = %+v, want nothing held and no plant named", items[2])
	}
	if !slices.Equal(items[3].HeldBarcodes, []string{oldest}) || items[3].ChosenBarcode != nil {
		t.Errorf("line without a choice = %+v, want the oldest plant, %s", items[3], oldest)
	}

	// The cart's hold has done its job.
	var status string
	if err := s.db.QueryRow(t.Context(), "SELECT status FROM plant_holds WHERE cart_token = 'c1-a'").Scan(&status); err != nil || status != "ordered" {
		t.Errorf("cart hold: %q %v, want ordered", status, err)
	}
}
