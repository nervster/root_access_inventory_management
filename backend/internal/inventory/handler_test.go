package inventory

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
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
)

// nursery is a test organization with an Owner, Staff, and Viewer, plus a member of another nursery.
type nursery struct {
	t     *testing.T
	db    db.Conn
	org   dbgen.Organization
	other dbgen.Organization    // a second nursery
	users map[string]dbgen.User // "owner", "staff", "viewer" (this nursery), "rival" (Owner of other)

	photos *fakeStorage // stands in for S3
}

func newNursery(t *testing.T) *nursery {
	t.Helper()
	tx := dbtest.Tx(t)
	q := dbgen.New(tx)
	ctx := t.Context()

	n := &nursery{t: t, db: tx, users: map[string]dbgen.User{}, photos: &fakeStorage{files: map[string]storage.FileInfo{}}}
	for i, slug := range []string{"inventory-test", "inventory-rival"} {
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
		"staff":  {n.org.ID, dbgen.OrgRoleStaff},
		"viewer": {n.org.ID, dbgen.OrgRoleViewer},
		"rival":  {n.other.ID, dbgen.OrgRoleOwner},
	}
	for name, m := range members {
		user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_inv_" + name, Email: name + "@inventory.example.com"})
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

// expect sends a request signed in as the named user to the nursery's (or, for "rival", the other
// nursery's) org path, and checks the status. {org} in path is replaced by the org ID.
func (n *nursery) expect(as, method, path, body string, wantStatus int) *httptest.ResponseRecorder {
	n.t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	signedIn := router.Group("/api", func(c *gin.Context) {
		auth.SetCurrentUser(c, n.users[as], false)
		c.Next()
	})
	Routes(signedIn, organization.NewStore(n.db), NewStore(n.db), n.photos)

	orgID := n.org.ID
	if as == "rival" {
		orgID = n.other.ID
	}
	path = strings.ReplaceAll(path, "{org}", fmt.Sprint(orgID))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != wantStatus {
		n.t.Fatalf("%s %s %s as %s: status = %d, want %d (body: %s)", method, path, body, as, recorder.Code, wantStatus, recorder.Body)
	}
	return recorder
}

func (n *nursery) addPotSize(as, body string) potSizeResponse {
	n.t.Helper()
	var size potSizeResponse
	if err := json.Unmarshal(n.expect(as, "POST", "/api/orgs/{org}/pot-sizes", body, http.StatusCreated).Body.Bytes(), &size); err != nil {
		n.t.Fatal(err)
	}
	return size
}

func (n *nursery) potSizes(as string) []potSizeResponse {
	n.t.Helper()
	var sizes []potSizeResponse
	if err := json.Unmarshal(n.expect(as, "GET", "/api/orgs/{org}/pot-sizes", "", http.StatusOK).Body.Bytes(), &sizes); err != nil {
		n.t.Fatal(err)
	}
	return sizes
}

func TestPotSizes(t *testing.T) {
	n := newNursery(t)

	tc := n.addPotSize("staff", `{"name":" Tissue Culture Cup ","diameterInches":1.5,"heightInches":2,"sellable":false}`)
	small := n.addPotSize("staff", `{"name":"Small Cup","diameterInches":3,"description":"3-inch cups"}`)
	if tc.Name != "Tissue Culture Cup" || tc.Sellable || tc.SortOrder != 0 {
		t.Errorf("tissue culture cup = %+v, want trimmed name, not sellable, first", tc)
	}
	if !small.Sellable || small.SortOrder != 1 || small.HeightInches != nil {
		t.Errorf("small cup = %+v, want sellable by default, second, no height", small)
	}

	sizes := n.potSizes("viewer")
	if len(sizes) != 2 || sizes[0].ID != tc.ID || sizes[1].ID != small.ID {
		t.Fatalf("pot sizes = %+v, want tissue culture cup then small cup", sizes)
	}

	// Leaving fields out keeps them; an empty description clears it.
	var updated potSizeResponse
	_ = json.Unmarshal(n.expect("staff", "PATCH", fmt.Sprintf("/api/orgs/{org}/pot-sizes/%d", small.ID),
		`{"heightInches":3.5,"description":""}`, http.StatusOK).Body.Bytes(), &updated)
	if updated.Name != "Small Cup" || *updated.DiameterInches != 3 || *updated.HeightInches != 3.5 || *updated.Description != "" {
		t.Errorf("updated = %+v, want only height and description changed", updated)
	}

	// Reorder: small first. Every pot size must be listed exactly once.
	n.expect("staff", "PUT", "/api/orgs/{org}/pot-sizes/order", fmt.Sprintf(`{"ids":[%d,%d]}`, small.ID, tc.ID), http.StatusOK)
	if sizes := n.potSizes("staff"); sizes[0].ID != small.ID {
		t.Errorf("after reordering, first = %q, want Small Cup", sizes[0].Name)
	}
	n.expect("staff", "PUT", "/api/orgs/{org}/pot-sizes/order", fmt.Sprintf(`{"ids":[%d]}`, small.ID), http.StatusBadRequest)
	n.expect("staff", "PUT", "/api/orgs/{org}/pot-sizes/order", fmt.Sprintf(`{"ids":[%d,%d]}`, small.ID, small.ID), http.StatusBadRequest)
}

func TestPotSizeRules(t *testing.T) {
	n := newNursery(t)
	n.addPotSize("owner", `{"name":"Small Cup"}`)

	n.expect("viewer", "POST", "/api/orgs/{org}/pot-sizes", `{"name":"Big"}`, http.StatusForbidden)    // needs inventory.edit
	n.expect("staff", "POST", "/api/orgs/{org}/pot-sizes", `{"name":"small cup"}`, http.StatusCreated) // names are case-sensitive
	n.expect("staff", "POST", "/api/orgs/{org}/pot-sizes", `{"name":"Small Cup"}`, http.StatusConflict)
	n.expect("staff", "POST", "/api/orgs/{org}/pot-sizes", `{"name":"  "}`, http.StatusBadRequest)
	n.expect("staff", "POST", "/api/orgs/{org}/pot-sizes", `{"diameterInches":2}`, http.StatusBadRequest)
	n.expect("staff", "POST", "/api/orgs/{org}/pot-sizes", `{"name":"Flat","heightInches":0}`, http.StatusBadRequest)
	n.expect("staff", "PATCH", "/api/orgs/{org}/pot-sizes/999999", `{"name":"X"}`, http.StatusNotFound)
}

func TestPotSizesStayInTheirNursery(t *testing.T) {
	n := newNursery(t)
	small := n.addPotSize("owner", `{"name":"Small Cup"}`)
	path := fmt.Sprintf("/api/orgs/{org}/pot-sizes/%d", small.ID)

	// The rival nursery's Owner can't see, change, or delete it, even knowing its ID.
	var theirs []potSizeResponse
	_ = json.Unmarshal(n.expect("rival", "GET", "/api/orgs/{org}/pot-sizes", "", http.StatusOK).Body.Bytes(), &theirs)
	if len(theirs) != 0 {
		t.Errorf("rival sees %+v, want none of this nursery's pot sizes", theirs)
	}
	n.expect("rival", "PATCH", path, `{"name":"Hacked"}`, http.StatusNotFound)
	n.expect("rival", "DELETE", path, "", http.StatusNotFound)
	n.expect("rival", "PUT", "/api/orgs/{org}/pot-sizes/order", fmt.Sprintf(`{"ids":[%d]}`, small.ID), http.StatusBadRequest)
}

func TestDeletePotSize(t *testing.T) {
	n := newNursery(t)
	unused := n.addPotSize("owner", `{"name":"Unused"}`)
	used := n.addPotSize("owner", `{"name":"Small Cup"}`)

	// A plant in the Small Cup.
	_, err := n.db.Exec(n.t.Context(), `
		WITH batch AS (
			INSERT INTO batches (organization_id, code, plant_name_id, received_on)
			SELECT $1, 'B-0001', id, current_date FROM plant_names WHERE name = 'Dragon Scale' AND organization_id IS NULL
			RETURNING id, plant_name_id)
		INSERT INTO plants (organization_id, barcode, batch_id, plant_name_id, pot_size_id)
		SELECT $1, 'TC-000001', id, plant_name_id, $2 FROM batch`, n.org.ID, used.ID)
	if err != nil {
		t.Fatal(err)
	}

	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/pot-sizes/%d", unused.ID), "", http.StatusNoContent)
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/pot-sizes/%d", used.ID), "", http.StatusConflict)
	if sizes := n.potSizes("staff"); len(sizes) != 1 || sizes[0].ID != used.ID {
		t.Errorf("pot sizes = %+v, want only the Small Cup left", sizes)
	}
}
