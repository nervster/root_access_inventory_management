package shopify

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/orders"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
)

type myPickupResponse struct {
	Key     string
	Error   string
	Message string
	Order   myPickupOrder
}

func (p *pickupTest) myPickup(path, body string, want int) myPickupResponse {
	p.t.Helper()
	recorder := p.proxyRequest("POST", path, testShop, "test-secret", nil, body)
	if recorder.Code != want {
		p.t.Fatalf("POST %s %s: %d (%s), want %d", path, body, recorder.Code, recorder.Body, want)
	}
	var r myPickupResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &r)
	return r
}

// bookedOrder places and imports an order from jane@example.com booked into the morning window.
func (p *pickupTest) bookedOrder(name string) (map[string]any, int64) {
	p.t.Helper()
	hold := p.hold("cart-" + name)
	order := p.order(name, map[string]string{"Pickup date": p.date.Format(time.DateOnly), "Pickup window": p.morning.Name, "Pickup hold": hold.ID})
	order["email"] = "jane@example.com"
	p.importOrders()
	var id int64
	if err := p.db.QueryRow(p.t.Context(), "SELECT id FROM orders WHERE name = $1 AND organization_id = $2", name, p.org.ID).Scan(&id); err != nil {
		p.t.Fatal(err)
	}
	return order, id
}

func (p *pickupTest) lookup(name string) myPickupResponse {
	p.t.Helper()
	return p.myPickup("/my-pickup/lookup", fmt.Sprintf(`{"orderNumber":%q,"email":" Jane@Example.COM "}`, name), 200)
}

func TestMyPickupLookup(t *testing.T) {
	p := newPickupTest(t)
	p.bookedOrder("#1001")

	// A wrong email and a wrong number get the same answer.
	wrongEmail := p.myPickup("/my-pickup/lookup", `{"orderNumber":"1001","email":"someone@else.com"}`, 404)
	wrongNumber := p.myPickup("/my-pickup/lookup", `{"orderNumber":"1999","email":"jane@example.com"}`, 404)
	if wrongEmail.Error != "not_found" || wrongEmail.Message != wrongNumber.Message {
		t.Errorf("wrong email = %+v, wrong number = %+v; want the same not_found", wrongEmail, wrongNumber)
	}
	p.myPickup("/my-pickup/lookup", `{"orderNumber":"10a1","email":"jane@example.com"}`, 400)
	p.myPickup("/my-pickup/lookup", `{"orderNumber":"1001"}`, 400)

	// The number with or without #, the email in any case: the order and a key. Nothing about the customer.
	found := p.lookup("1001")
	got := found.Order
	if found.Key == "" || got.Order != "#1001" || got.Status != "open" || !got.CanChangeTime || !got.CanCancel || got.CancelUntil != nil ||
		got.Pickup == nil || got.Pickup.Window != p.morning.Name || got.Pickup.Start.Hour() != 8 {
		t.Fatalf("lookup = %+v, want #1001 open, changeable, cancellable, booked in the morning (8 AM Houston)", found)
	}
	if again := p.lookup("#1001"); again.Order.Order != "#1001" {
		t.Errorf("with # = %+v", again)
	}

	// The key brings the order back; a made-up or altered key doesn't.
	if current := p.myPickup("/my-pickup", fmt.Sprintf(`{"key":%q}`, found.Key), 200); current.Order.Order != "#1001" {
		t.Errorf("current = %+v", current)
	}
	if r := p.myPickup("/my-pickup", `{"key":"mp_made.up"}`, 401); r.Error != "expired" {
		t.Errorf("made-up key = %+v, want expired", r)
	}
	p.myPickup("/my-pickup", fmt.Sprintf(`{"key":%q}`, found.Key+"x"), 401)

	// Without the Email field, Shopify withholds it: unavailable, not "not found".
	p.fake.emailWithheld = true
	if r := p.myPickup("/my-pickup/lookup", `{"orderNumber":"1001","email":"jane@example.com"}`, 503); r.Error != "unavailable" {
		t.Errorf("email withheld = %+v, want unavailable", r)
	}
}

func TestLookupLimiter(t *testing.T) {
	limiter, now := newLookupLimiter(), time.Now()
	for i := range 10 {
		if !limiter.allow("1/9.9.9.9", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("lookup %d refused, want 10 allowed", i+1)
		}
	}
	if limiter.allow("1/9.9.9.9", now.Add(time.Minute)) {
		t.Error("an 11th lookup within 15 minutes was allowed")
	}
	if !limiter.allow("1/8.8.8.8", now.Add(time.Minute)) {
		t.Error("another visitor was refused")
	}
	if !limiter.allow("1/9.9.9.9", now.Add(16*time.Minute)) {
		t.Error("after 15 minutes, still refused")
	}
}

func TestMyPickupChangeTime(t *testing.T) {
	p := newPickupTest(t)
	shopifyOrder, orderID := p.bookedOrder("#1001")
	key := p.lookup("1001").Key
	rules, _ := p.slots.Rules(t.Context(), p.org.ID)
	evening := rules.Windows[2]

	// Another cart holds the next day's morning: taken.
	nextDay := p.date.AddDate(0, 0, 1)
	if _, err := p.slots.PlaceHold(t.Context(), p.org.ID, "cart-x", "", nextDay, p.morning.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if r := p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":%q,"windowId":%d}`, key, nextDay.Format(time.DateOnly), p.morning.ID), 409); r.Error != "taken" {
		t.Errorf("change to a held slot = %+v, want taken", r)
	}

	// The evening: moved, the morning freed for others.
	changed := p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":%q,"windowId":%d}`, key, p.date.Format(time.DateOnly), evening.ID), 200)
	if changed.Order.Pickup == nil || changed.Order.Pickup.Window != evening.Name {
		t.Fatalf("after change = %+v, want the evening", changed.Order)
	}
	if p.slotStatus() != pickup.StatusOpen || p.bookings("booked") != 1 {
		t.Errorf("morning = %s with %d bookings, want it free and one booking", p.slotStatus(), p.bookings("booked"))
	}
	// The same slot again: nothing changes.
	p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":%q,"windowId":%d}`, key, p.date.Format(time.DateOnly), evening.ID), 200)

	// An import before Shopify's copy is updated keeps the new time.
	shopifyOrder["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	p.importOrders()
	if o := p.ordersPickup("open")["#1001"]; o.PickupWindow == nil || *o.PickupWindow != evening.Name {
		t.Errorf("after an import = %+v, want the evening kept", o)
	}
	// The job updates Shopify's attributes, keeping the others.
	if err := p.store.UpdateOrderPickup(t.Context(), p.org.ID, orderID); err != nil {
		t.Fatal(err)
	}
	attributes := map[string]string{}
	for _, a := range shopifyOrder["customAttributes"].([]map[string]string) {
		attributes[a["key"]] = a["value"]
	}
	if attributes["Pickup window"] != evening.Name || attributes["Pickup date"] != p.date.Format(time.DateOnly) || attributes["Pickup hold"] == "" {
		t.Errorf("Shopify's attributes = %v, want the new window and date, and the hold kept", attributes)
	}
	if p.waitingJobs(orders.JobUpdateOrderPickup, 0) == 0 {
		t.Error("no job queued to update Shopify")
	}

	// Too soon or closed, like the cart.
	if r := p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":"2020-01-01","windowId":%d}`, key, p.morning.ID), 409); r.Error != "too_soon" {
		t.Errorf("a past date = %+v, want too_soon", r)
	}
}

func TestMyPickupAfterReady(t *testing.T) {
	p := newPickupTest(t)
	_, orderID := p.bookedOrder("#1001")
	key := p.lookup("1001").Key
	p.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/ready", orderID), 204)

	// Ready: cancellable for 2 hours after.
	got := p.myPickup("/my-pickup", fmt.Sprintf(`{"key":%q}`, key), 200).Order
	if got.Status != "ready_for_pickup" || !got.CanCancel || got.CancelUntil == nil || time.Until(*got.CancelUntil) < 119*time.Minute {
		t.Errorf("just ready = %+v, want cancellable for 2 hours", got)
	}

	// 3 hours later: no cancelling, but the time can still change, and the order stays ready.
	if _, err := p.db.Exec(t.Context(), "UPDATE orders SET ready_at = now() - interval '3 hours' WHERE id = $1", orderID); err != nil {
		t.Fatal(err)
	}
	r := p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, key), 409)
	if r.Error != "too_late" || r.Order.CanCancel || !r.Order.CanChangeTime {
		t.Errorf("cancel after 3 hours ready = %+v, want too_late with the order (changeable, not cancellable)", r)
	}
	rules, _ := p.slots.Rules(t.Context(), p.org.ID)
	changed := p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":%q,"windowId":%d}`, key, p.date.Format(time.DateOnly), rules.Windows[2].ID), 200)
	if changed.Order.Status != "ready_for_pickup" {
		t.Errorf("changed while ready = %+v, want still ready", changed.Order)
	}
	var changedAt *time.Time
	_ = p.db.QueryRow(t.Context(), "SELECT pickup_changed_at FROM orders WHERE id = $1", orderID).Scan(&changedAt)
	if changedAt == nil {
		t.Error("pickup_changed_at not set, want staff to see the change")
	}

	// Picked up: nothing more.
	p.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/picked-up", orderID), 204)
	if r := p.myPickup("/my-pickup/change", fmt.Sprintf(`{"key":%q,"date":%q,"windowId":%d}`, key, p.date.Format(time.DateOnly), p.morning.ID), 409); r.Error != "too_late" {
		t.Errorf("change after pickup = %+v, want too_late", r)
	}
}

func TestMyPickupCancel(t *testing.T) {
	p := newPickupTest(t)
	shopifyOrder, _ := p.bookedOrder("#1001")
	key := p.lookup("1001").Key

	// Shopify refusing: unavailable, nothing cancelled here.
	p.fake.cancelRefusal = "Order has an active return"
	p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, key), 503)
	if p.bookings("booked") != 1 {
		t.Fatal("a refused cancellation freed the slot")
	}
	p.fake.cancelRefusal = ""

	got := p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, key), 200).Order
	if got.Status != "cancelled" || got.CanCancel || got.CanChangeTime {
		t.Errorf("cancelled = %+v, want cancelled with nothing left to do", got)
	}
	if shopifyOrder["cancelledAt"] == nil || shopifyOrder["refunded"] != true {
		t.Errorf("Shopify order = cancelled %v, refunded %v; want both", shopifyOrder["cancelledAt"], shopifyOrder["refunded"])
	}
	var held int
	_ = p.db.QueryRow(t.Context(), "SELECT count(*) FROM plants WHERE status = 'held' AND organization_id = $1", p.org.ID).Scan(&held)
	if held != 0 || p.slotStatus() != pickup.StatusOpen {
		t.Errorf("after cancelling: %d plants held, slot %s; want back on sale and the slot free", held, p.slotStatus())
	}
	// The next import agrees (Shopify has it cancelled too) and changes nothing.
	p.importOrders()
	if r := p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, key), 409); r.Error != "too_late" {
		t.Errorf("cancelling again = %+v, want too_late", r)
	}
}
