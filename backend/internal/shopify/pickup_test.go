package shopify

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/orders"
)

type pickupOrderJSON struct {
	ID                          int64
	Name, Status                string
	ReadyByName, PickedUpByName *string
}

// ordersIn lists orders with status, as the named user sees them.
func (s *shopTest) ordersIn(as, status string) []pickupOrderJSON {
	s.t.Helper()
	var list []pickupOrderJSON
	_ = json.Unmarshal(s.ordersRequest(as, "GET", "/orders?status="+status, 200).Body.Bytes(), &list)
	return list
}

func (s *shopTest) ordersRequest(as, method, path string, wantStatus int) *httptest.ResponseRecorder {
	s.t.Helper()
	recorder := httptest.NewRecorder()
	s.ordersRouter(as).ServeHTTP(recorder, httptest.NewRequest(method, fmt.Sprintf("/api/orgs/%d%s", s.org.ID, path), nil))
	if recorder.Code != wantStatus {
		s.t.Fatalf("%s %s as %s: %d, want %d (%s)", method, path, as, recorder.Code, wantStatus, recorder.Body)
	}
	return recorder
}

// orderedPlant connects the store, puts one plant on sale, and imports an order for it.
func (s *shopTest) orderedPlant() (order pickupOrderJSON, plant string) {
	s.t.Helper()
	s.connect()
	product, _ := s.addProduct(0, "Picked Up", map[string]*int{"Small Cup": cents(1500)})
	plant = s.addPlants(product, "Small Cup", "for_sale", 1)[0]
	s.sync(product)
	s.placeOrder("#1001", map[string]int{"Small Cup": 1})
	s.importOrders()
	return s.ordersIn("viewer", "open")[0], plant
}

func TestPickup(t *testing.T) {
	s := newShopTest(t)
	order, plant := s.orderedPlant()
	path := fmt.Sprintf("/orders/%d", order.ID)

	// In the locker: ready for pickup. Viewers can see orders but not change them.
	s.ordersRequest("viewer", "POST", path+"/ready", 403)
	s.ordersRequest("staff", "POST", path+"/ready", 204)
	s.ordersRequest("staff", "POST", path+"/ready", 409) // not waiting to be prepared any more
	ready := s.ordersIn("viewer", "ready_for_pickup")
	if len(ready) != 1 || *ready[0].ReadyByName != "Pat staff" || len(s.ordersIn("viewer", "open")) != 0 || s.plantStatus(plant) != "held" {
		t.Fatalf("ready orders %+v, want #1001 marked by Pat staff, its plant still held", ready)
	}

	// Marked ready by mistake: back to "to prepare", and ready again.
	s.ordersRequest("viewer", "POST", path+"/not-ready", 403)
	s.ordersRequest("staff", "POST", path+"/not-ready", 204)
	if open := s.ordersIn("viewer", "open"); len(open) != 1 || open[0].ReadyByName != nil {
		t.Fatalf("open orders %+v, want #1001 back, no longer marked ready", open)
	}
	s.ordersRequest("staff", "POST", path+"/not-ready", 409)
	s.ordersRequest("staff", "POST", path+"/ready", 204)

	// Picked up: the worker fulfills it in Shopify first; until that works, the plant stays held.
	s.ordersRequest("staff", "POST", path+"/picked-up", 204)
	if n := s.waitingJobs(orders.JobFulfillOrder, 0); n != 1 {
		t.Fatalf("%d fulfill jobs queued, want 1", n)
	}
	s.fake.fulfillFail = true
	if err := s.store.FulfillOrder(t.Context(), s.org.ID, order.ID); err == nil || s.plantStatus(plant) != "held" {
		t.Fatalf("fulfilling failed: %v, plant %s; want an error (retried) and the plant still held", err, s.plantStatus(plant))
	}
	s.fake.fulfillFail = false
	if err := s.store.FulfillOrder(t.Context(), s.org.ID, order.ID); err != nil {
		t.Fatal(err)
	}
	shopifyID := s.fake.orders[0]["id"].(string)
	if !s.fake.fulfilled[shopifyID] || s.plantStatus(plant) != "sold" {
		t.Errorf("fulfilled on Shopify %v, plant %s; want fulfilled (without emailing the customer) and sold", s.fake.fulfilled[shopifyID], s.plantStatus(plant))
	}
	picked := s.ordersIn("viewer", "picked_up")
	if len(picked) != 1 || *picked[0].PickedUpByName != "Pat staff" {
		t.Errorf("picked-up orders %+v, want #1001 by Pat staff", picked)
	}

	// Running it again (a repeated job), or importing the now-fulfilled order, changes nothing.
	if err := s.store.FulfillOrder(t.Context(), s.org.ID, order.ID); err != nil {
		t.Error(err)
	}
	s.importOrders()
	if s.plantStatus(plant) != "sold" || len(s.ordersIn("viewer", "picked_up")) != 1 {
		t.Error("repeating the pickup changed something")
	}
	s.ordersRequest("staff", "POST", path+"/picked-up", 409)
	s.ordersRequest("staff", "POST", path+"/not-ready", 409) // picked up is final
	s.ordersRequest("staff", "POST", "/orders/999999/ready", 404)
}

func TestPickedUpInPerson(t *testing.T) {
	s := newShopTest(t)
	order, plant := s.orderedPlant()
	// A bigger order skips the locker: picked up straight from "to prepare".
	s.ordersRequest("owner", "POST", fmt.Sprintf("/orders/%d/picked-up", order.ID), 204)
	if err := s.store.FulfillOrder(t.Context(), s.org.ID, order.ID); err != nil || s.plantStatus(plant) != "sold" {
		t.Errorf("in-person pickup: %v, plant %s; want sold", err, s.plantStatus(plant))
	}
}

func TestCancelledWhileInTheLocker(t *testing.T) {
	s := newShopTest(t)
	order, plant := s.orderedPlant()
	s.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/ready", order.ID), 204)

	s.fake.orders[0]["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
	s.importOrders()
	if s.plantStatus(plant) != "for_sale" || len(s.ordersIn("viewer", "cancelled")) != 1 {
		t.Errorf("plant %s, want back on sale and the order cancelled", s.plantStatus(plant))
	}
	s.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/picked-up", order.ID), 409)
}
