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

func TestMyPickupItemsAndAmounts(t *testing.T) {
	p := newPickupTest(t)
	order, _ := p.bookedOrder("#1001")
	line := order["lineItems"].(map[string]any)["nodes"].([]map[string]any)[0]
	line["variantTitle"] = `6" pot`
	line["discountedTotal"] = 1400              // $15.00 less a $1.00 code discount: only with withCodeDiscounts
	order["subtotalPriceSet"] = money("105.00") // Shopify's subtotal: after discounts
	order["totalDiscountsSet"] = money("10.00")
	order["totalTaxSet"] = money("8.66")
	order["totalPriceSet"] = money("113.66")

	found := p.lookup("1001")
	items, amounts := found.Order.Items, found.Order.Amounts
	if len(items) != 1 || items[0].Title == "" || items[0].Variant == nil || *items[0].Variant != `6" pot` || items[0].Quantity != 1 || items[0].TotalCents != 1400 {
		t.Errorf("items = %+v, want the line with its variant and its total after the code discount", items)
	}
	want := myPickupAmounts{Currency: "USD", SubtotalCents: 11500, DiscountCents: 1000, TaxCents: 866, TotalCents: 11366}
	if amounts == nil || *amounts != want {
		t.Errorf("amounts = %+v, want %+v (subtotal before discounts, so the page's lines add up)", amounts, want)
	}

	// A product's default variant has no variant to show.
	line["variantTitle"] = "Default Title"
	if got := p.myPickup("/my-pickup", fmt.Sprintf(`{"key":%q}`, found.Key), 200).Order.Items; len(got) != 1 || got[0].Variant != nil {
		t.Errorf("default variant = %+v, want variant null", got)
	}

	// Shopify not answering: the order still comes back, without what was ordered and paid.
	p.fake.emailWithheld = true
	if got := p.myPickup("/my-pickup", fmt.Sprintf(`{"key":%q}`, found.Key), 200).Order; got.Items != nil || got.Amounts != nil || got.Order != "#1001" {
		t.Errorf("without Shopify = %+v, want the order without items or amounts", got)
	}
	p.fake.emailWithheld = false

	// After cancelling, the refund shows.
	if got := p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, found.Key), 200).Order.Amounts; got == nil || got.RefundedCents != 11366 {
		t.Errorf("after cancelling: amounts = %+v, want 11366 refunded", got)
	}
}

// finishCancels completes the cancels Shopify was processing in the background: each order is cancelled, and
// its units, committed until now and not restocked (restock: false), leave on hand at the location.
func (f *fakeShopify) finishCancels(location string) {
	for _, order := range f.pendingCancels {
		now := time.Now().UTC().Format(time.RFC3339)
		order["cancelledAt"], order["updatedAt"] = now, now
		for _, line := range order["lineItems"].(map[string]any)["nodes"].([]map[string]any) {
			for _, product := range f.products {
				for _, v := range product.Variants {
					if v.ID == line["variant"].(map[string]string)["id"] {
						f.onHand[v.InventoryItemID+"@"+location] -= line["quantity"].(int)
					}
				}
			}
		}
	}
	f.pendingCancels = nil
}

// A My pickup cancel puts the plants back on sale (and syncs stock) at once, but Shopify finishes the cancel
// later, taking the order's units off on hand. Its cancelled webhook must sync stock again.
func TestMyPickupCancelSyncsStockOnceShopifyIsDone(t *testing.T) {
	p := newPickupTest(t)
	p.chooseLocation(greenhouse)
	var productID int64
	if err := p.db.QueryRow(t.Context(), "SELECT id FROM products WHERE organization_id = $1", p.org.ID).Scan(&productID); err != nil {
		t.Fatal(err)
	}
	_, _ = p.bookedOrder("#1001")
	if err := p.syncStock(productID); err != nil || p.onHand("Small Cup", greenhouse) != 6 {
		t.Fatalf("before: %v, on hand %d; want 6 (5 for sale, 1 held)", err, p.onHand("Small Cup", greenhouse))
	}

	p.fake.cancelLater = true
	p.myPickup("/my-pickup/cancel", fmt.Sprintf(`{"key":%q}`, p.lookup("1001").Key), 200)
	if err := p.syncStock(productID); err != nil || p.onHand("Small Cup", greenhouse) != 6 {
		t.Fatalf("after the cancel: %v, on hand %d; want 6, all for sale", err, p.onHand("Small Cup", greenhouse))
	}

	// Shopify finishes: the order's committed unit leaves on hand, and the cancelled webhook queues an import.
	p.fake.finishCancels(greenhouse)
	if p.onHand("Small Cup", greenhouse) != 5 {
		t.Fatalf("Shopify's on hand %d after its cancel, want 5 (the fake should take the unit off)", p.onHand("Small Cup", greenhouse))
	}
	if _, err := p.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}
	p.importOrders()
	if p.waitingJobs(JobSyncStock, productID) != 1 {
		t.Fatalf("%d stock syncs queued when Shopify reported the cancel, want 1", p.waitingJobs(JobSyncStock, productID))
	}
	if err := p.syncStock(productID); err != nil || p.onHand("Small Cup", greenhouse) != 6 {
		t.Errorf("after the sync: %v, on hand %d; want 6 again", err, p.onHand("Small Cup", greenhouse))
	}

	// Later imports of the cancelled order don't queue more.
	if _, err := p.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}
	p.importOrders()
	if n := p.waitingJobs(JobSyncStock, productID); n != 0 {
		t.Errorf("%d stock syncs queued on re-reading the cancelled order, want 0", n)
	}
}
