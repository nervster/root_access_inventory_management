package organization

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

func TestPlatformIsForPlatformAdminsOnly(t *testing.T) {
	n := newNursery(t)

	n.expect("owner", "GET", "/api/platform/organizations", "", http.StatusForbidden)
	n.expect("owner", "GET", "/api/platform/users", "", http.StatusForbidden)

	var me meResponse
	_ = json.Unmarshal(n.expect("platform", "GET", "/api/me", "", http.StatusOK).Body.Bytes(), &me)
	if !me.IsPlatformAdmin {
		t.Error("isPlatformAdmin = false for a platform admin")
	}
	_ = json.Unmarshal(n.expect("owner", "GET", "/api/me", "", http.StatusOK).Body.Bytes(), &me)
	if me.IsPlatformAdmin {
		t.Error("isPlatformAdmin = true for a nursery Owner")
	}

	// Platform admins aren't members, so a nursery's own area stays closed to them.
	n.expect("platform", "GET", "/api/orgs/{org}", "", http.StatusNotFound)
}

func TestPlatformCreateOrganization(t *testing.T) {
	n := newNursery(t)

	recorder := n.expect("platform", "POST", "/api/platform/organizations",
		`{"name":"Root Access HTX","ownerEmail":"founder@example.com"}`, http.StatusCreated)
	var created createOrganizationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Organization.Slug != "root-access-htx" || created.Organization.TimeZone != "America/Chicago" {
		t.Errorf("organization = %+v, want slug root-access-htx in America/Chicago", created.Organization)
	}
	if created.OwnerInvitation.Role != dbgen.OrgRoleOwner || created.OwnerInvitation.Email != "founder@example.com" || created.DeliveryError != nil {
		t.Errorf("invitation = %+v (delivery error %v), want an Owner invitation sent to founder@example.com", created.OwnerInvitation, created.DeliveryError)
	}
	if len(n.signUp.invited) != 1 || n.signUp.invited[0] != "founder@example.com" {
		t.Errorf("Clerk invited %v, want founder@example.com", n.signUp.invited)
	}

	n.expect("platform", "POST", "/api/platform/organizations", `{"name":"Root Access HTX","ownerEmail":"a@example.com"}`, http.StatusConflict) // slug taken
	n.expect("platform", "POST", "/api/platform/organizations", `{"name":"X","slug":"Bad Slug","ownerEmail":"a@example.com"}`, http.StatusBadRequest)
	n.expect("platform", "POST", "/api/platform/organizations", `{"name":"X","timeZone":"Nowhere","ownerEmail":"a@example.com"}`, http.StatusBadRequest)
	n.expect("platform", "POST", "/api/platform/organizations", `{"name":"X","ownerEmail":"nope"}`, http.StatusBadRequest)

	var orgs []platformOrganizationResponse
	_ = json.Unmarshal(n.expect("platform", "GET", "/api/platform/organizations", "", http.StatusOK).Body.Bytes(), &orgs)
	if len(orgs) != 2 || orgs[0].Name != "Root Access HTX" || orgs[0].MemberCount != 0 || orgs[1].MemberCount != 4 {
		t.Errorf("organizations = %+v, want Root Access HTX (0 members) and Test Nursery (4), by name", orgs)
	}
}

func TestPlatformSuspendOrganization(t *testing.T) {
	n := newNursery(t)

	n.expect("platform", "PATCH", "/api/platform/organizations/{org}", `{"status":"closed"}`, http.StatusBadRequest)
	n.expect("platform", "PATCH", "/api/platform/organizations/999999", `{"status":"suspended"}`, http.StatusNotFound)

	n.expect("platform", "PATCH", "/api/platform/organizations/{org}", `{"status":"suspended"}`, http.StatusOK)
	n.expect("owner", "GET", "/api/orgs/{org}", "", http.StatusForbidden)

	n.expect("platform", "PATCH", "/api/platform/organizations/{org}", `{"status":"active"}`, http.StatusOK)
	n.expect("owner", "GET", "/api/orgs/{org}", "", http.StatusOK)
}

func TestPlatformFixesMemberships(t *testing.T) {
	n := newNursery(t)
	base := "/api/platform/organizations/{org}"

	var detail platformOrganizationDetailResponse
	_ = json.Unmarshal(n.expect("platform", "GET", base, "", http.StatusOK).Body.Bytes(), &detail)
	if len(detail.Members) != 4 || detail.Organization.MemberCount != 4 {
		t.Errorf("detail = %+v, want 4 members", detail)
	}

	// The same rules as inside the nursery: the last Owner can't be demoted...
	n.expect("platform", "PATCH", base+"/members/{owner}", `{"role":"staff"}`, http.StatusConflict)
	// ...but a platform admin can name a new Owner, remove anyone, and invite any role.
	n.expect("platform", "PATCH", base+"/members/{staff}", `{"role":"owner"}`, http.StatusOK)
	n.expect("platform", "DELETE", base+"/members/{viewer}", "", http.StatusNoContent)
	recorder := n.expect("platform", "POST", base+"/invitations", `{"email":"new-owner@example.com","role":"owner"}`, http.StatusCreated)
	var invitation invitationResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	n.expect("platform", "DELETE", fmt.Sprintf("%s/invitations/%d", base, invitation.ID), "", http.StatusNoContent)
}

func TestPlatformSearchUsers(t *testing.T) {
	n := newNursery(t)

	var users []platformUserResponse
	_ = json.Unmarshal(n.expect("platform", "GET", "/api/platform/users?email=STAFF", "", http.StatusOK).Body.Bytes(), &users)
	if len(users) != 1 || users[0].Email != "staff@example.com" {
		t.Fatalf("users = %+v, want staff@example.com", users)
	}
	if len(users[0].Memberships) != 1 || users[0].Memberships[0].Role != dbgen.OrgRoleStaff {
		t.Errorf("memberships = %+v, want staff in Test Nursery", users[0].Memberships)
	}

	_ = json.Unmarshal(n.expect("platform", "GET", "/api/platform/users?email=platform", "", http.StatusOK).Body.Bytes(), &users)
	if len(users) != 1 || !users[0].IsPlatformAdmin || len(users[0].Memberships) != 0 {
		t.Errorf("users = %+v, want the platform admin with no memberships", users)
	}

	_ = json.Unmarshal(n.expect("platform", "GET", "/api/platform/users", "", http.StatusOK).Body.Bytes(), &users)
	if len(users) != 6 {
		t.Errorf("got %d users with no search term, want all 6", len(users))
	}
}
