package shopify

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
)

// pickupTest is a connected store selling one product, with pickup windows 12 AM, 8 AM and 4 PM.
type pickupTest struct {
	*shopTest
	slots   *pickup.Store
	date    time.Time // three days from now, Houston
	morning pickup.Window
}

func newPickupTest(t *testing.T) *pickupTest {
	t.Helper()
	p := &pickupTest{shopTest: newShopTest(t)}
	p.connect()
	product, _ := p.addProduct(0, "Picked up", map[string]*int{"Small Cup": cents(1500)})
	p.addPlants(product, "Small Cup", "for_sale", 6)
	p.sync(product)
	p.slots = pickup.NewStore(p.db)
	if err := p.slots.SetUpWindows(t.Context(), p.org.ID, 0, 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	rules, _ := p.slots.Rules(t.Context(), p.org.ID)
	p.morning = rules.Windows[1]
	today := time.Now().In(rules.Location)
	p.date = pickup.Date(today.Year(), today.Month(), today.Day()+3)
	return p
}

// order places an order whose cart saved these attributes (as the theme does).
func (p *pickupTest) order(name string, attributes map[string]string) map[string]any {
	p.t.Helper()
	order := p.placeOrder(name, map[string]int{"Small Cup": 1})
	var list []map[string]string
	for k, v := range attributes {
		list = append(list, map[string]string{"key": k, "value": v})
	}
	order["customAttributes"] = list
	return order
}

func (p *pickupTest) hold(cart string) pickup.Hold {
	p.t.Helper()
	hold, err := p.slots.PlaceHold(p.t.Context(), p.org.ID, cart, "", p.date, p.morning.ID, time.Now())
	if err != nil {
		p.t.Fatal(err)
	}
	return hold
}

type orderPickupJSON struct {
	Name                                                string
	PickupDate, PickupWindow, PickupNote, PickupProblem *string
}

func (p *pickupTest) ordersPickup(status string) map[string]orderPickupJSON {
	p.t.Helper()
	recorder := httptest.NewRecorder()
	p.ordersRouter("admin").ServeHTTP(recorder, httptest.NewRequest("GET", fmt.Sprintf("/api/orgs/%d/orders?status=%s", p.org.ID, status), nil))
	var list []orderPickupJSON
	_ = json.Unmarshal(recorder.Body.Bytes(), &list)
	out := map[string]orderPickupJSON{}
	for _, o := range list {
		out[o.Name] = o
	}
	return out
}

// slotStatus is the morning slot's status for a cart that holds nothing.
func (p *pickupTest) slotStatus() string {
	p.t.Helper()
	_, days, err := p.slots.Calendar(p.t.Context(), p.org.ID, p.date, p.date, time.Now(), "")
	if err != nil {
		p.t.Fatal(err)
	}
	return days[0].Slots[1].Status
}

func (p *pickupTest) bookings(status string) int {
	p.t.Helper()
	var n int
	if err := p.db.QueryRow(p.t.Context(), "SELECT count(*) FROM pickup_bookings WHERE organization_id = $1 AND status = $2", p.org.ID, status).Scan(&n); err != nil {
		p.t.Fatal(err)
	}
	return n
}

func TestOrderTurnsItsHoldIntoABooking(t *testing.T) {
	p := newPickupTest(t)
	hold := p.hold("cart-a")
	date := p.date.Format(time.DateOnly)
	order := p.order("#1001", map[string]string{"Pickup date": date, "Pickup window": p.morning.Name, "Pickup hold": hold.ID})
	order["note"] = " Gate code 1234 " // the cart note
	p.importOrders()

	got := p.ordersPickup("open")["#1001"]
	if got.PickupDate == nil || *got.PickupDate != date || *got.PickupWindow != p.morning.Name || *got.PickupNote != "Gate code 1234" || got.PickupProblem != nil {
		t.Errorf("order = %+v, want its date, window and note, and no problem", got)
	}
	if p.bookings("booked") != 1 || p.bookings("held") != 0 || p.slotStatus() != pickup.StatusTaken {
		t.Errorf("booked %d, held %d, slot %s; want the hold booked and the slot taken", p.bookings("booked"), p.bookings("held"), p.slotStatus())
	}
	// A booking doesn't run out like a hold, and importing again changes nothing.
	p.importOrders()
	if p.bookings("booked") != 1 {
		t.Errorf("after importing again: %d bookings, want 1", p.bookings("booked"))
	}

	// Cancelled: the slot is free again.
	p.fake.orders[0]["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
	p.importOrders()
	if p.bookings("cancelled") != 1 || p.slotStatus() != pickup.StatusOpen {
		t.Errorf("after cancelling: %d cancelled, slot %s; want the booking cancelled and the slot open", p.bookings("cancelled"), p.slotStatus())
	}
}

func TestOrderPickupProblems(t *testing.T) {
	p := newPickupTest(t)
	date := p.date.Format(time.DateOnly)

	// No hold, but a free date and window (an older cart): booked directly.
	p.order("#1001", map[string]string{"Pickup date": date, "Pickup window": p.morning.Name})
	// The same slot, by another order without a hold: a clash for staff.
	p.order("#1002", map[string]string{"Pickup date": date, "Pickup window": p.morning.Name})
	// A window Plant Stock doesn't have.
	p.order("#1003", map[string]string{"Pickup date": date, "Pickup window": "Lunch"})
	// A hold Plant Stock doesn't know, and nothing else to go by.
	p.order("#1004", map[string]string{"Pickup hold": "ph_unknown"})
	// No pickup attributes at all (in-person pickup): nothing to book, no problem.
	p.order("#1005", nil)
	p.importOrders()

	orders := p.ordersPickup("open")
	if o := orders["#1001"]; o.PickupProblem != nil || p.bookings("booked") != 1 {
		t.Errorf("#1001 = %+v with %d bookings, want booked without a problem", o, p.bookings("booked"))
	}
	wants := map[string]string{"#1002": "Another customer has", "#1003": `no pickup window called "Lunch"`, "#1004": "no pickup date and window"}
	for name, want := range wants {
		if o := orders[name]; o.PickupProblem == nil || !strings.Contains(*o.PickupProblem, want) {
			t.Errorf("%s problem = %v, want it to mention %q", name, o.PickupProblem, want)
		}
	}
	if o := orders["#1005"]; o.PickupProblem != nil || o.PickupDate != nil {
		t.Errorf("#1005 = %+v, want no pickup and no problem", o)
	}

	// Once #1001 is cancelled, the next import books #1002 into the freed slot and clears its problem.
	p.fake.orders[0]["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
	p.importOrders()
	if o := p.ordersPickup("open")["#1002"]; o.PickupProblem != nil {
		t.Errorf("#1002 after the slot freed = %+v, want booked without a problem", o)
	}
}

func TestOrderWhoseHoldWasTakenByAnother(t *testing.T) {
	p := newPickupTest(t)
	hold := p.hold("cart-a")
	// The hold runs out while the shopper pays, and another cart takes the slot.
	if _, err := p.db.Exec(t.Context(), "UPDATE pickup_bookings SET expires_at = now() - interval '1 minute' WHERE hold_token = $1", hold.ID); err != nil {
		t.Fatal(err)
	}
	p.hold("cart-b")
	p.order("#1001", map[string]string{"Pickup date": p.date.Format(time.DateOnly), "Pickup window": p.morning.Name, "Pickup hold": hold.ID})
	p.importOrders()
	if o := p.ordersPickup("open")["#1001"]; o.PickupProblem == nil || !strings.Contains(*o.PickupProblem, "Another customer has") {
		t.Errorf("order = %+v, want a clash for staff", o)
	}
}

func TestPrivacyRequestsClearPickupNotes(t *testing.T) {
	p := newPickupTest(t)
	order := p.order("#1001", map[string]string{"Pickup date": p.date.Format(time.DateOnly), "Pickup window": p.morning.Name})
	order["note"] = "Call me"
	p.importOrders()
	orderID := strings.TrimPrefix(order["id"].(string), "gid://shopify/Order/")
	if code := p.webhook("customers/redact", testShop, `{"orders_to_redact": [`+orderID+`]}`, true); code != 200 {
		t.Fatalf("redact: %d", code)
	}
	if o := p.ordersPickup("open")["#1001"]; o.PickupNote != nil || o.PickupWindow == nil {
		t.Errorf("after the privacy request = %+v, want the note gone and the window kept", o)
	}
}
