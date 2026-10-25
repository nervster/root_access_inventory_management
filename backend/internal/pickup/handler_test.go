package pickup

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
)

// nursery is a test organization with an Owner, Admin, Staff and Viewer, plus the Owner of another
// nursery ("rival"), and a clock the test sets.
type nursery struct {
	t     *testing.T
	db    db.Conn
	org   dbgen.Organization
	other dbgen.Organization
	users map[string]dbgen.User
	now   time.Time
}

func newNursery(t *testing.T) *nursery {
	t.Helper()
	tx := dbtest.Tx(t)
	q := dbgen.New(tx)
	ctx := t.Context()
	loc, _ := time.LoadLocation("America/Chicago")
	n := &nursery{t: t, db: tx, users: map[string]dbgen.User{}, now: time.Date(2026, 10, 23, 16, 0, 0, 0, loc)}
	for i, slug := range []string{"pickup-test", "pickup-rival"} {
		org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			n.org = org
		} else {
			n.other = org
		}
	}
	members := map[string]struct {
		org  int64
		role dbgen.OrgRole
	}{
		"owner":  {n.org.ID, dbgen.OrgRoleOwner},
		"admin":  {n.org.ID, dbgen.OrgRoleAdmin},
		"staff":  {n.org.ID, dbgen.OrgRoleStaff},
		"viewer": {n.org.ID, dbgen.OrgRoleViewer},
		"rival":  {n.other.ID, dbgen.OrgRoleOwner},
	}
	for name, m := range members {
		user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_pickup_" + name, Email: name + "@pickup.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateMembership(ctx, dbgen.CreateMembershipParams{OrganizationID: m.org, UserID: user.ID, Role: m.role}); err != nil {
			t.Fatal(err)
		}
		n.users[name] = user
	}
	return n
}

// expect sends a request as the named user to their nursery ({org} in path), and checks the status.
func (n *nursery) expect(as, method, path, body string, wantStatus int) *httptest.ResponseRecorder {
	n.t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	signedIn := router.Group("/api", func(c *gin.Context) {
		auth.SetCurrentUser(c, n.users[as], false)
		c.Next()
	})
	routes(signedIn, organization.NewStore(n.db), NewStore(n.db), func() time.Time { return n.now })
	orgID := n.org.ID
	if as == "rival" {
		orgID = n.other.ID
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, strings.ReplaceAll(path, "{org}", fmt.Sprint(orgID)), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		n.t.Fatalf("%s %s %s as %s: status = %d, want %d (body: %s)", method, path, body, as, recorder.Code, wantStatus, recorder.Body)
	}
	return recorder
}

func (n *nursery) settings(as string) settingsResponse {
	n.t.Helper()
	var s settingsResponse
	_ = json.Unmarshal(n.expect(as, "GET", "/api/orgs/{org}/pickup", "", 200).Body.Bytes(), &s)
	return s
}

func (n *nursery) calendar(query string) calendarResponse {
	n.t.Helper()
	var c calendarResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/pickup/calendar"+query, "", 200).Body.Bytes(), &c)
	return c
}

// statuses is each day's slot statuses, e.g. {"2026-10-24": ["open", "open", "open"]}.
func (c calendarResponse) statuses() map[string][]string {
	out := map[string][]string{}
	for _, d := range c.Days {
		for _, s := range d.Slots {
			out[d.Date] = append(out[d.Date], s.Status)
		}
	}
	return out
}

func TestSetUpWindows(t *testing.T) {
	n := newNursery(t)
	if s := n.settings("viewer"); len(s.Windows) != 0 || s.LeadHours != 8 || s.BookingDays != 30 || s.TimeZone != "America/Chicago" {
		t.Errorf("new nursery = %+v, want no windows, 8 hours lead, 30 days", s)
	}

	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":6}`, 403)
	n.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":0}`, 400)
	n.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8}`, 400)
	n.expect("admin", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":6}`, 200)
	s := n.settings("viewer")
	if len(s.Windows) != 4 || s.Windows[0].Name != "2 AM – 8 AM" || s.Windows[1].Name != "8 AM – 2 PM" {
		t.Fatalf("windows = %+v, want 4 six-hour windows from 8 AM, sorted by start", s.Windows)
	}

	// Names are the nursery's to change, and stay unique.
	morning := s.Windows[1].ID
	n.expect("owner", "PATCH", fmt.Sprintf("/api/orgs/{org}/pickup/windows/%d", morning), `{"name":"  Morning "}`, 200)
	n.expect("owner", "PATCH", fmt.Sprintf("/api/orgs/{org}/pickup/windows/%d", s.Windows[2].ID), `{"name":"Morning"}`, 409)
	n.expect("owner", "PATCH", fmt.Sprintf("/api/orgs/{org}/pickup/windows/%d", morning), `{"name":"  "}`, 400)
	n.expect("staff", "PATCH", fmt.Sprintf("/api/orgs/{org}/pickup/windows/%d", morning), `{"name":"Nope"}`, 403)
	n.expect("rival", "PATCH", fmt.Sprintf("/api/orgs/{org}/pickup/windows/%d", morning), `{"name":"Nope"}`, 404)
	if got := n.settings("viewer").Windows[1].Name; got != "Morning" {
		t.Errorf("renamed window = %q, want Morning", got)
	}

	// Lead time and booking range.
	n.expect("owner", "PATCH", "/api/orgs/{org}/pickup", `{"leadHours":12}`, 200)
	n.expect("owner", "PATCH", "/api/orgs/{org}/pickup", `{"bookingDays":0}`, 400)
	n.expect("owner", "PATCH", "/api/orgs/{org}/pickup", `{"leadHours":-1}`, 400)
	n.expect("staff", "PATCH", "/api/orgs/{org}/pickup", `{"leadHours":4}`, 403)
	if s := n.settings("viewer"); s.LeadHours != 12 || s.BookingDays != 30 {
		t.Errorf("limits = %d hours, %d days; want 12 and 30", s.LeadHours, s.BookingDays)
	}
	// The other nursery's are its own.
	if s := n.settings("rival"); len(s.Windows) != 0 || s.LeadHours != 8 {
		t.Errorf("rival = %+v, want untouched defaults", s)
	}
}

func TestCalendarLeadTimeAndRange(t *testing.T) {
	n := newNursery(t)
	n.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":0,"hours":8}`, 200) // 12 AM, 8 AM, 4 PM

	// Today is Oct 23, 4:00 PM: every window today is too soon; tomorrow's 12 AM window is just bookable.
	c := n.calendar("")
	if c.Today != "2026-10-23" || len(c.Days) != 30 || c.Days[0].Date != "2026-10-23" || c.Days[29].Date != "2026-11-21" {
		t.Fatalf("default range: today %s, %d days from %s; want today and 30 days", c.Today, len(c.Days), c.Days[0].Date)
	}
	got := c.statuses()
	if want := "[too_soon too_soon too_soon]"; fmt.Sprint(got["2026-10-23"]) != want {
		t.Errorf("today = %v, want %s", got["2026-10-23"], want)
	}
	if want := "[open open open]"; fmt.Sprint(got["2026-10-24"]) != want {
		t.Errorf("tomorrow at 4:00 PM = %v, want %s", got["2026-10-24"], want)
	}
	n.now = n.now.Add(time.Minute)
	if want := "[too_soon open open]"; fmt.Sprint(n.calendar("").statuses()["2026-10-24"]) != want {
		t.Errorf("tomorrow at 4:01 PM = %v, want %s", n.calendar("").statuses()["2026-10-24"], want)
	}

	// Past the booking range: too far.
	got = n.calendar("?from=2026-11-21&to=2026-11-22").statuses()
	if fmt.Sprint(got["2026-11-21"]) != "[open open open]" || fmt.Sprint(got["2026-11-22"]) != "[too_far too_far too_far]" {
		t.Errorf("range edge = %v, want Nov 21 open and Nov 22 too far", got)
	}

	n.expect("viewer", "GET", "/api/orgs/{org}/pickup/calendar?from=2026-10-25&to=2026-10-24", "", 400)
	n.expect("viewer", "GET", "/api/orgs/{org}/pickup/calendar?from=2026-10-01&to=2026-12-15", "", 400)
	n.expect("viewer", "GET", "/api/orgs/{org}/pickup/calendar?from=Oct25", "", 400)
}

func TestClosures(t *testing.T) {
	n := newNursery(t)
	n.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":0,"hours":8}`, 200)
	windows := n.settings("viewer").Windows
	morning := windows[1].ID // 8 AM – 4 PM

	// Staff close a whole day, with a reason, and one window on another day.
	n.expect("viewer", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"2026-10-25","closed":true}`, 403)
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"2026-10-25","closed":true,"reason":" Out of town "}`, 204)
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", fmt.Sprintf(`{"date":"2026-10-26","windowId":%d,"closed":true}`, morning), 204)
	// Closing again is fine.
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"2026-10-25","closed":true,"reason":"Out of town"}`, 204)

	c := n.calendar("?from=2026-10-25&to=2026-10-26")
	day := c.Days[0]
	if day.Closure == nil || *day.Closure.Reason != "Out of town" || *day.Closure.ClosedBy != "staff@pickup.example.com" {
		t.Errorf("Oct 25 closure = %+v, want the reason and who closed it", day.Closure)
	}
	if got := fmt.Sprint(c.statuses()); got != "map[2026-10-25:[closed closed closed] 2026-10-26:[open closed open]]" {
		t.Errorf("statuses = %s, want Oct 25 all closed, Oct 26 only the morning", got)
	}
	if c.Days[1].Slots[1].Closure == nil || c.Days[1].Slots[0].Closure != nil {
		t.Errorf("Oct 26 = %+v, want the closure on the morning window only", c.Days[1].Slots)
	}

	// Reopening.
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", fmt.Sprintf(`{"date":"2026-10-26","windowId":%d,"closed":false}`, morning), 204)
	if got := fmt.Sprint(n.calendar("?from=2026-10-26&to=2026-10-26").statuses()); got != "map[2026-10-26:[open open open]]" {
		t.Errorf("after reopening = %s, want all open", got)
	}

	// Another nursery's window, or none: not found.
	n.expect("rival", "PUT", "/api/orgs/{org}/pickup/closures", fmt.Sprintf(`{"date":"2026-10-26","windowId":%d,"closed":true}`, morning), 404)
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"2026-10-26","windowId":999999999,"closed":true}`, 404)
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"26 Oct","closed":true}`, 400)
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", `{"date":"2026-10-26"}`, 400)

	// Setting up new windows drops the old windows' closures but keeps whole-day ones.
	n.expect("staff", "PUT", "/api/orgs/{org}/pickup/closures", fmt.Sprintf(`{"date":"2026-10-27","windowId":%d,"closed":true}`, morning), 204)
	n.expect("owner", "PUT", "/api/orgs/{org}/pickup/windows", `{"startHour":8,"hours":12}`, 200)
	if got := fmt.Sprint(n.calendar("?from=2026-10-25&to=2026-10-27").statuses()); got != "map[2026-10-25:[closed closed] 2026-10-26:[open open] 2026-10-27:[open open]]" {
		t.Errorf("after new windows = %s, want Oct 25 still closed, the rest open", got)
	}
}
