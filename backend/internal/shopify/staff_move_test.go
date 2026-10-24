package shopify

import (
	"fmt"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/orders"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
)

func (p *pickupTest) move(as string, orderID int64, date time.Time, windowID int64, want int) {
	p.t.Helper()
	p.ordersRequestBody(as, "POST", fmt.Sprintf("/orders/%d/pickup", orderID), fmt.Sprintf(`{"date":%q,"windowId":%d}`, date.Format(time.DateOnly), windowID), want)
}

func TestStaffMovePickup(t *testing.T) {
	p := newPickupTest(t)
	_, first := p.bookedOrder("#1001")
	// #1002 named the same slot without a hold: a clash for staff.
	p.order("#1002", map[string]string{"Pickup date": p.date.Format(time.DateOnly), "Pickup window": p.morning.Name})
	p.importOrders()
	var second int64
	_ = p.db.QueryRow(t.Context(), "SELECT id FROM orders WHERE name = '#1002' AND organization_id = $1", p.org.ID).Scan(&second)
	if o := p.ordersPickup("open")["#1002"]; o.PickupProblem == nil {
		t.Fatalf("#1002 = %+v, want a clash to sort out", o)
	}
	rules, _ := p.slots.Rules(t.Context(), p.org.ID)
	evening := rules.Windows[2]

	// Staff move it to the evening: booked, problem gone, Shopify's copy to update.
	p.move("staff", second, p.date, evening.ID, 204)
	if o := p.ordersPickup("open")["#1002"]; o.PickupProblem != nil || o.PickupWindow == nil || *o.PickupWindow != evening.Name {
		t.Errorf("after the move = %+v, want the evening and no problem", o)
	}
	if p.bookings("booked") != 2 || p.waitingJobs(orders.JobUpdateOrderPickup, 0) == 0 {
		t.Errorf("%d bookings, %d update jobs; want 2 and one queued", p.bookings("booked"), p.waitingJobs(orders.JobUpdateOrderPickup, 0))
	}

	// Staff may book the window running right now (a customer couldn't: it's within the lead time).
	now := time.Now().In(rules.Location)
	today := pickup.Date(now.Year(), now.Month(), now.Day())
	current := rules.Windows[now.Hour()/8]
	p.move("staff", first, today, current.ID, 204)

	// But not a closed window, a taken one, or one that has ended.
	if err := p.slots.Close(t.Context(), p.org.ID, p.date.AddDate(0, 0, 1), nil, nil); err != nil {
		t.Fatal(err)
	}
	p.move("staff", second, p.date.AddDate(0, 0, 1), evening.ID, 409)
	p.move("staff", second, today, current.ID, 409)                   // #1001 has it now
	p.move("staff", second, today.AddDate(0, 0, -1), evening.ID, 409) // yesterday
	p.move("staff", second, p.date, 999999999, 404)

	// Viewers can't; picked-up orders can't be moved.
	p.move("viewer", second, p.date, p.morning.ID, 403)
	p.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/picked-up", second), 204)
	p.move("staff", second, p.date, p.morning.ID, 409)
}
