package organization

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// nursery is a test organization with one member of each role and a user from outside it.
type nursery struct {
	t     *testing.T
	db    db.Conn
	org   dbgen.Organization
	users map[string]dbgen.User // "owner", "admin", "staff", "viewer", "outsider"
	ids   map[string]int64      // membership IDs by the same names
}

func newNursery(t *testing.T) *nursery {
	t.Helper()
	tx := dbtest.Tx(t)
	q := dbgen.New(tx)
	ctx := t.Context()

	org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: "Test Nursery", Slug: "test-nursery", TimeZone: "America/Chicago"})
	if err != nil {
		t.Fatal(err)
	}

	n := &nursery{t: t, db: tx, org: org, users: map[string]dbgen.User{}, ids: map[string]int64{}}
	for _, name := range []string{"owner", "admin", "staff", "viewer", "outsider"} {
		user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_" + name, Email: name + "@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		n.users[name] = user
		if name == "outsider" {
			continue
		}
		membership, err := q.CreateMembership(ctx, dbgen.CreateMembershipParams{OrganizationID: org.ID, UserID: user.ID, Role: dbgen.OrgRole(name)})
		if err != nil {
			t.Fatal(err)
		}
		n.ids[name] = membership.ID
	}
	return n
}

// request sends a request signed in as the named user and returns the response.
func (n *nursery) request(as, method, path, body string) *httptest.ResponseRecorder {
	n.t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	signedIn := router.Group("/api", func(c *gin.Context) {
		auth.SetCurrentUser(c, n.users[as])
		c.Next()
	})
	Routes(signedIn, NewStore(n.db))

	path = strings.ReplaceAll(path, "{org}", fmt.Sprint(n.org.ID))
	for name, id := range n.ids {
		path = strings.ReplaceAll(path, "{"+name+"}", fmt.Sprint(id))
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

func (n *nursery) expect(as, method, path, body string, wantStatus int) *httptest.ResponseRecorder {
	n.t.Helper()
	recorder := n.request(as, method, path, body)
	if recorder.Code != wantStatus {
		n.t.Fatalf("%s %s %s as %s: status = %d, want %d (body: %s)", method, path, body, as, recorder.Code, wantStatus, recorder.Body)
	}
	return recorder
}

func TestOrganizationAccess(t *testing.T) {
	n := newNursery(t)

	n.expect("outsider", "GET", "/api/orgs/{org}", "", http.StatusNotFound) // non-members can't tell it exists
	n.expect("staff", "GET", "/api/orgs/999999", "", http.StatusNotFound)
	n.expect("staff", "GET", "/api/orgs/abc", "", http.StatusNotFound)
	n.expect("viewer", "GET", "/api/orgs/{org}", "", http.StatusOK)
	n.expect("admin", "PATCH", "/api/orgs/{org}", `{"name":"Renamed"}`, http.StatusForbidden) // organization.manage is Owner-only

	if _, err := n.db.Exec(t.Context(), "UPDATE organizations SET status = 'suspended' WHERE id = $1", n.org.ID); err != nil {
		t.Fatal(err)
	}
	n.expect("owner", "GET", "/api/orgs/{org}", "", http.StatusForbidden)
}

func TestUpdateOrganization(t *testing.T) {
	n := newNursery(t)

	recorder := n.expect("owner", "PATCH", "/api/orgs/{org}", `{"name":"  Root Access HTX ","timeZone":"America/Denver"}`, http.StatusOK)
	var org organizationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	if org.Name != "Root Access HTX" || org.TimeZone != "America/Denver" {
		t.Errorf("got %+v, want name trimmed and time zone changed", org)
	}

	// Leaving a field out keeps it.
	recorder = n.expect("owner", "PATCH", "/api/orgs/{org}", `{"timeZone":"America/Chicago"}`, http.StatusOK)
	if err := json.Unmarshal(recorder.Body.Bytes(), &org); err != nil {
		t.Fatal(err)
	}
	if org.Name != "Root Access HTX" {
		t.Errorf("Name = %q, want it unchanged", org.Name)
	}

	n.expect("owner", "PATCH", "/api/orgs/{org}", `{"timeZone":"Mars/Olympus"}`, http.StatusBadRequest)
	n.expect("owner", "PATCH", "/api/orgs/{org}", `{"name":"   "}`, http.StatusBadRequest)
}

func TestListMembers(t *testing.T) {
	n := newNursery(t)

	recorder := n.expect("viewer", "GET", "/api/orgs/{org}/members", "", http.StatusOK)
	var members []memberResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members[0].Role != dbgen.OrgRoleOwner {
		t.Errorf("got %+v, want 4 members with the Owner first", members)
	}
}

func TestChangeRole(t *testing.T) {
	n := newNursery(t)

	n.expect("admin", "PATCH", "/api/orgs/{org}/members/{staff}", `{"role":"admin"}`, http.StatusForbidden) // roles.manage is Owner-only
	n.expect("owner", "PATCH", "/api/orgs/{org}/members/{staff}", `{"role":"boss"}`, http.StatusBadRequest)
	n.expect("owner", "PATCH", "/api/orgs/{org}/members/999999", `{"role":"admin"}`, http.StatusNotFound)

	// The only Owner can't step down until someone else is an Owner.
	n.expect("owner", "PATCH", "/api/orgs/{org}/members/{owner}", `{"role":"admin"}`, http.StatusConflict)
	n.expect("owner", "PATCH", "/api/orgs/{org}/members/{admin}", `{"role":"owner"}`, http.StatusOK)
	recorder := n.expect("owner", "PATCH", "/api/orgs/{org}/members/{owner}", `{"role":"admin"}`, http.StatusOK)

	var member memberResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &member); err != nil {
		t.Fatal(err)
	}
	if member.Role != dbgen.OrgRoleAdmin || member.Email != "owner@example.com" {
		t.Errorf("got %+v, want owner@example.com now an admin", member)
	}
}

func TestRemoveMember(t *testing.T) {
	n := newNursery(t)

	n.expect("staff", "DELETE", "/api/orgs/{org}/members/{viewer}", "", http.StatusForbidden) // staff can't remove others
	n.expect("admin", "DELETE", "/api/orgs/{org}/members/{owner}", "", http.StatusForbidden)  // admins can't remove Owners
	n.expect("owner", "DELETE", "/api/orgs/{org}/members/{owner}", "", http.StatusConflict)   // the last Owner can't leave
	n.expect("admin", "DELETE", "/api/orgs/{org}/members/{viewer}", "", http.StatusNoContent)
	n.expect("staff", "DELETE", "/api/orgs/{org}/members/{staff}", "", http.StatusNoContent) // anyone can leave
	n.expect("staff", "GET", "/api/orgs/{org}", "", http.StatusNotFound)                     // and then they're out
}

func TestMe(t *testing.T) {
	n := newNursery(t)

	recorder := n.expect("admin", "GET", "/api/me", "", http.StatusOK)
	var me meResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Email != "admin@example.com" || len(me.Memberships) != 1 {
		t.Fatalf("got %+v, want admin@example.com with one membership", me)
	}
	membership := me.Memberships[0]
	if membership.OrganizationID != n.org.ID || membership.Role != dbgen.OrgRoleAdmin {
		t.Errorf("membership = %+v, want admin of %d", membership, n.org.ID)
	}
	for _, p := range membership.Permissions {
		if p == FinancialsView {
			t.Errorf("admin permissions include %s, want it Owner-only", p)
		}
	}

	recorder = n.expect("outsider", "GET", "/api/me", "", http.StatusOK)
	if err := json.Unmarshal(recorder.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Memberships == nil || len(me.Memberships) != 0 {
		t.Errorf("Memberships = %v, want an empty list (not null)", me.Memberships)
	}
}
