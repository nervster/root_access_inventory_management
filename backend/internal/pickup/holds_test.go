package pickup

import (
	"errors"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// holdTest is a nursery with windows 12 AM, 8 AM and 4 PM (8 hours each), and the clock at Oct 23, 4 PM.
type holdTest struct {
	*nursery
	store                      *Store
	midnight, morning, evening int64
	tomorrow                   time.Time
}

func newHoldTest(t *testing.T) *holdTest {
	t.Helper()
	h := &holdTest{nursery: newNursery(t), store: nil, tomorrow: Date(2026, 10, 24)}
	h.store = NewStore(h.db)
	if err := h.store.SetUpWindows(t.Context(), h.org.ID, 0, 8, h.now); err != nil {
		t.Fatal(err)
	}
	rules, err := h.store.Rules(t.Context(), h.org.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.midnight, h.morning, h.evening = rules.Windows[0].ID, rules.Windows[1].ID, rules.Windows[2].ID
	return h
}

func (h *holdTest) place(cart, ip string, date time.Time, windowID int64) (Hold, error) {
	return h.store.PlaceHold(h.t.Context(), h.org.ID, cart, ip, date, windowID, h.now)
}

// statusFor is a slot's status as cart sees it ("" for staff).
func (h *holdTest) statusFor(cart string, date time.Time, windowID int64) string {
	h.t.Helper()
	_, days, err := h.store.Calendar(h.t.Context(), h.org.ID, date, date, h.now, cart)
	if err != nil {
		h.t.Fatal(err)
	}
	for _, s := range days[0].Slots {
		if s.ID == windowID {
			return s.Status
		}
	}
	h.t.Fatalf("window %d not on %s", windowID, date.Format(time.DateOnly))
	return ""
}

func TestHoldTakesTheSlot(t *testing.T) {
	h := newHoldTest(t)
	hold, err := h.place("cart-a", "1.1.1.1", h.tomorrow, h.morning)
	if err != nil {
		t.Fatal(err)
	}
	if len(hold.ID) < 20 || hold.Window != "8 AM – 4 PM" || !hold.ExpiresAt.Equal(h.now.Add(30*time.Minute)) ||
		!hold.Start.Equal(time.Date(2026, 10, 24, 8, 0, 0, 0, h.now.Location())) { // 8 AM Houston, whatever the machine's zone
		t.Errorf("hold = %+v, want a long random ID, the window, 8 AM, and 30 minutes", hold)
	}
	// Taken for everyone else (staff see it taken too); still open for the cart that holds it.
	if got := h.statusFor("cart-b", h.tomorrow, h.morning); got != StatusTaken {
		t.Errorf("for another cart: %s, want taken", got)
	}
	if got := h.statusFor("", h.tomorrow, h.morning); got != StatusTaken {
		t.Errorf("for staff: %s, want taken", got)
	}
	if got := h.statusFor("cart-a", h.tomorrow, h.morning); got != StatusOpen {
		t.Errorf("for its own cart: %s, want open", got)
	}
	if _, err := h.place("cart-b", "2.2.2.2", h.tomorrow, h.morning); !errors.Is(err, ErrTaken) {
		t.Errorf("another cart holding it: %v, want taken", err)
	}

	// The same slot again: renewed, same ID. Another slot: the first is given back.
	h.now = h.now.Add(10 * time.Minute)
	again, err := h.place("cart-a", "1.1.1.1", h.tomorrow, h.morning)
	if err != nil || again.ID != hold.ID || !again.ExpiresAt.Equal(h.now.Add(30*time.Minute)) {
		t.Errorf("same slot again = %+v, %v; want the same hold, renewed", again, err)
	}
	if _, err := h.place("cart-a", "1.1.1.1", h.tomorrow, h.evening); err != nil {
		t.Fatal(err)
	}
	if got := h.statusFor("cart-b", h.tomorrow, h.morning); got != StatusOpen {
		t.Errorf("after the cart chose another slot: %s, want the first open again", got)
	}
}

func TestHoldChecksTheCalendar(t *testing.T) {
	h := newHoldTest(t)
	if err := h.store.Close(t.Context(), h.org.ID, h.tomorrow, &h.evening, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		date     time.Time
		windowID int64
		want     error
	}{
		{"closed window", h.tomorrow, h.evening, ErrClosed},
		{"today, too soon", Date(2026, 10, 23), h.evening, ErrTooSoon},
		{"past the booking range", Date(2026, 11, 30), h.morning, ErrTooFar},
		{"no such window", h.tomorrow, 999999999, ErrNotFound},
	}
	for _, c := range cases {
		if _, err := h.place("cart-a", "", c.date, c.windowID); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

func TestHoldExpiresAndRefreshes(t *testing.T) {
	h := newHoldTest(t)
	hold, err := h.place("cart-a", "", h.tomorrow, h.morning)
	if err != nil {
		t.Fatal(err)
	}

	// Refreshing before it runs out keeps it 30 more minutes.
	h.now = h.now.Add(20 * time.Minute)
	refreshed, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now)
	if err != nil || !refreshed.ExpiresAt.Equal(h.now.Add(30*time.Minute)) {
		t.Fatalf("refresh = %+v, %v; want 30 more minutes", refreshed, err)
	}
	// Another cart's token can't refresh it.
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-b", hold.ID, h.now); !errors.Is(err, ErrNotFound) {
		t.Errorf("refresh with another cart's token: %v, want not found", err)
	}

	// Once it runs out, the slot is free for others…
	h.now = h.now.Add(31 * time.Minute)
	if got := h.statusFor("cart-b", h.tomorrow, h.morning); got != StatusOpen {
		t.Errorf("after it ran out: %s, want open", got)
	}
	// …and a refresh takes it back while nobody else has.
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now); err != nil {
		t.Errorf("refreshing a hold that ran out, slot still free: %v, want it taken back", err)
	}
	h.now = h.now.Add(31 * time.Minute)
	if _, err := h.place("cart-b", "", h.tomorrow, h.morning); err != nil {
		t.Fatalf("another cart after it ran out again: %v", err)
	}
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now); !errors.Is(err, ErrTaken) {
		t.Errorf("refresh after another cart took it: %v, want taken", err)
	}
}

func TestRefreshChecksTheLeadTimeAgain(t *testing.T) {
	h := newHoldTest(t)
	// At 3:50 PM, tomorrow's 12 AM window starts 8 hours 10 minutes from now: bookable.
	h.now = h.now.Add(-10 * time.Minute)
	hold, err := h.place("cart-a", "", h.tomorrow, h.midnight)
	if err != nil {
		t.Fatal(err)
	}
	// At Checkout 30 minutes later it's under 8 hours: the shopper must pick again.
	h.now = h.now.Add(30 * time.Minute)
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now); !errors.Is(err, ErrTooSoon) {
		t.Errorf("refresh under the lead time: %v, want too soon", err)
	}
	// Closing the window also stops a refresh.
	hold, _ = h.place("cart-a", "", h.tomorrow, h.morning)
	if err := h.store.Close(t.Context(), h.org.ID, h.tomorrow, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now); !errors.Is(err, ErrClosed) {
		t.Errorf("refresh after the day was closed: %v, want closed", err)
	}
}

func TestReleaseHold(t *testing.T) {
	h := newHoldTest(t)
	hold, _ := h.place("cart-a", "", h.tomorrow, h.morning)
	// Another cart's token can't release it.
	if err := h.store.ReleaseHold(t.Context(), h.org.ID, "cart-b", hold.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.statusFor("cart-b", h.tomorrow, h.morning); got != StatusTaken {
		t.Errorf("after another cart's release: %s, want still taken", got)
	}
	for range 2 { // releasing twice is fine
		if err := h.store.ReleaseHold(t.Context(), h.org.ID, "cart-a", hold.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.statusFor("cart-b", h.tomorrow, h.morning); got != StatusOpen {
		t.Errorf("after release: %s, want open", got)
	}
	if _, err := h.store.RefreshHold(t.Context(), h.org.ID, "cart-a", hold.ID, h.now); !errors.Is(err, ErrNotFound) {
		t.Errorf("refresh after release: %v, want not found", err)
	}
}

func TestHoldsPerVisitor(t *testing.T) {
	h := newHoldTest(t)
	slots := []struct {
		date     time.Time
		windowID int64
	}{{h.tomorrow, h.midnight}, {h.tomorrow, h.morning}, {h.tomorrow, h.evening}, {Date(2026, 10, 25), h.morning}}
	for i, cart := range []string{"cart-1", "cart-2", "cart-3"} {
		if _, err := h.place(cart, "9.9.9.9", slots[i].date, slots[i].windowID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.place("cart-4", "9.9.9.9", slots[3].date, slots[3].windowID); !errors.Is(err, ErrTooManyHolds) {
		t.Errorf("a 4th cart from the same visitor: %v, want too many holds", err)
	}
	// A cart changing its own slot isn't another hold; another visitor isn't limited.
	if _, err := h.place("cart-1", "9.9.9.9", slots[3].date, slots[3].windowID); err != nil {
		t.Errorf("a cart switching slots: %v", err)
	}
	if _, err := h.place("cart-5", "8.8.8.8", Date(2026, 10, 26), h.morning); err != nil {
		t.Errorf("another visitor: %v", err)
	}
}

func TestOneLiveHoldPerSlotInTheDatabase(t *testing.T) {
	h := newHoldTest(t)
	hold, err := h.place("cart-a", "", h.tomorrow, h.morning)
	if err != nil {
		t.Fatal(err)
	}
	// Even inserted directly (two requests racing past every check), the second live row is refused.
	q := dbgen.New(h.db)
	lockers, _ := q.ListPickupLockers(t.Context(), h.org.ID)
	_, err = q.CreatePickupHold(t.Context(), dbgen.CreatePickupHoldParams{
		OrganizationID: h.org.ID, LockerID: lockers[0].ID, Date: h.tomorrow, WindowID: &h.morning, WindowName: "x",
		StartsAt: hold.Start, EndsAt: hold.End, HoldToken: "ph_racer", CartToken: "cart-b", ExpiresAt: h.now.Add(time.Hour),
	})
	if !isUniqueViolation(err) {
		t.Errorf("a second live hold for the slot: %v, want the unique index to refuse it", err)
	}
}

func TestWindowsCantBeReplacedWithUpcomingHolds(t *testing.T) {
	h := newHoldTest(t)
	if _, err := h.place("cart-a", "", h.tomorrow, h.morning); err != nil {
		t.Fatal(err)
	}
	h.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":6}`, 409)
	// Once the hold has run out, they can be.
	h.now = h.now.Add(31 * time.Minute)
	h.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":6}`, 200)
}
