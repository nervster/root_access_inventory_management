package shopify

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

type readyByJSON struct {
	Name       string
	ReadyBy    *time.Time
	LockerCode *string
}

func (p *pickupTest) openOrdersAs(as string) []readyByJSON {
	p.t.Helper()
	recorder := httptest.NewRecorder()
	p.ordersRouter(as).ServeHTTP(recorder, httptest.NewRequest("GET", fmt.Sprintf("/api/orgs/%d/orders?status=open", p.org.ID), nil))
	var list []readyByJSON
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		p.t.Fatalf("orders as %s: %s", as, recorder.Body)
	}
	return list
}

func TestOrdersByReadyByWithLockerCodes(t *testing.T) {
	p := newPickupTest(t)
	rules, _ := p.slots.Rules(t.Context(), p.org.ID)
	evening := rules.Windows[2]
	date := p.date.Format(time.DateOnly)
	// Placed in this order, but due in the other: #1002's morning comes before #1001's evening.
	p.order("#1001", map[string]string{"Pickup date": date, "Pickup window": evening.Name})
	p.order("#1002", map[string]string{"Pickup date": date, "Pickup window": p.morning.Name})
	p.order("#1003", map[string]string{"Pickup date": date, "Pickup window": "Lunch"}) // no slot: needs staff
	p.importOrders()

	got := p.openOrdersAs("staff")
	if len(got) != 3 || got[0].Name != "#1003" || got[1].Name != "#1002" || got[2].Name != "#1001" {
		t.Fatalf("open orders = %+v, want #1003 (no slot) first, then #1002 (morning), then #1001 (evening)", got)
	}
	morningStart, _ := rules.Bounds(p.date, p.morning)
	if got[0].ReadyBy != nil || got[1].ReadyBy == nil || !got[1].ReadyBy.Equal(morningStart) {
		t.Errorf("ready by = %v and %v, want none for #1003 and the morning's start for #1002", got[0].ReadyBy, got[1].ReadyBy)
	}

	// Every order has a 4-digit locker code; staff see it, viewers don't.
	four := regexp.MustCompile(`^[0-9]{4}$`)
	for _, o := range got {
		if o.LockerCode == nil || !four.MatchString(*o.LockerCode) {
			t.Errorf("%s locker code = %v, want 4 digits", o.Name, o.LockerCode)
		}
	}
	for _, o := range p.openOrdersAs("viewer") {
		if o.LockerCode != nil {
			t.Errorf("a viewer sees %s's locker code", o.Name)
		}
	}
	// The code doesn't change on a later import.
	code := *got[1].LockerCode
	p.fake.orders[1]["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	p.importOrders()
	if again := p.openOrdersAs("staff")[1]; again.LockerCode == nil || *again.LockerCode != code {
		t.Errorf("after another import #1002's code = %v, want %s", again.LockerCode, code)
	}
}
