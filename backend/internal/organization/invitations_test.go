package organization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/email"
)

// fakeSignUp stands in for Clerk: it records sign-up invitations instead of sending them.
type fakeSignUp struct {
	registered map[string]bool // emails that already have an account
	invited    []string
}

func (f *fakeSignUp) InviteToSignUp(_ context.Context, address, _ string) (bool, error) {
	if f.registered[address] {
		return true, nil
	}
	f.invited = append(f.invited, address)
	return false, nil
}

// fakeMail records email instead of sending it, or fails when broken is set.
type fakeMail struct {
	sent   []email.Message
	broken bool
}

func (f *fakeMail) Send(_ context.Context, msg email.Message) error {
	if f.broken {
		return errors.New("smtp: connection refused")
	}
	f.sent = append(f.sent, msg)
	return nil
}

func (n *nursery) deliverer() Deliverer {
	return Deliverer{SignUp: n.signUp, Email: n.mail, AppURL: "http://localhost:5173"}
}

func (n *nursery) invite(as, address string, role dbgen.OrgRole, wantStatus int) invitationResponse {
	n.t.Helper()
	recorder := n.expect(as, "POST", "/api/orgs/{org}/invitations", fmt.Sprintf(`{"email":%q,"role":%q}`, address, role), wantStatus)
	var invitation invitationResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &invitation)
	return invitation
}

func TestInviteSomeoneNew(t *testing.T) {
	n := newNursery(t)

	invitation := n.invite("admin", "  New@Example.com", dbgen.OrgRoleStaff, http.StatusCreated)
	if invitation.Email != "new@example.com" || invitation.Expired {
		t.Errorf("got %+v, want a pending invitation for new@example.com", invitation)
	}
	// New people get Clerk's sign-up invitation, not our email.
	if len(n.signUp.invited) != 1 || n.signUp.invited[0] != "new@example.com" || len(n.mail.sent) != 0 {
		t.Errorf("Clerk invited %v, we emailed %v; want only a Clerk invitation", n.signUp.invited, n.mail.sent)
	}

	var open []invitationResponse
	_ = json.Unmarshal(n.expect("admin", "GET", "/api/orgs/{org}/invitations", "", http.StatusOK).Body.Bytes(), &open)
	if len(open) != 1 || open[0].ID != invitation.ID {
		t.Errorf("open invitations = %+v, want just the new one", open)
	}
}

func TestInviteSomeoneWithAnAccount(t *testing.T) {
	n := newNursery(t)
	n.signUp.registered["friend@example.com"] = true

	n.invite("owner", "friend@example.com", dbgen.OrgRoleAdmin, http.StatusCreated)

	if len(n.mail.sent) != 1 || !strings.Contains(n.mail.sent[0].Subject, "Test Nursery") {
		t.Fatalf("emailed %+v, want one email naming Test Nursery", n.mail.sent)
	}
	if !strings.Contains(n.mail.sent[0].Text, "as admin") {
		t.Errorf("email text = %q, want it to mention the role", n.mail.sent[0].Text)
	}
}

func TestInviteRules(t *testing.T) {
	n := newNursery(t)

	n.invite("staff", "x@example.com", dbgen.OrgRoleViewer, http.StatusForbidden) // needs team.manage
	n.invite("admin", "x@example.com", dbgen.OrgRoleOwner, http.StatusForbidden)  // admins invite Staff and Viewers only
	n.invite("admin", "x@example.com", dbgen.OrgRoleAdmin, http.StatusForbidden)  //
	n.invite("owner", "not an email", dbgen.OrgRoleStaff, http.StatusBadRequest)  //
	n.invite("owner", "Bob <bob@example.com>", dbgen.OrgRoleStaff, http.StatusBadRequest)
	n.invite("owner", "x@example.com", "boss", http.StatusBadRequest)
	n.invite("owner", "staff@example.com", dbgen.OrgRoleStaff, http.StatusConflict) // already a member
	n.invite("owner", "x@example.com", dbgen.OrgRoleStaff, http.StatusCreated)
	n.invite("owner", "X@example.com", dbgen.OrgRoleStaff, http.StatusConflict) // already invited (any case)
}

func TestInvitationDeliveryFailure(t *testing.T) {
	n := newNursery(t)
	n.signUp.registered["friend@example.com"] = true
	n.mail.broken = true

	n.invite("owner", "friend@example.com", dbgen.OrgRoleStaff, http.StatusBadGateway)

	// It was saved anyway, so it can be resent once email works.
	var open []invitationResponse
	_ = json.Unmarshal(n.expect("owner", "GET", "/api/orgs/{org}/invitations", "", http.StatusOK).Body.Bytes(), &open)
	if len(open) != 1 {
		t.Fatalf("open invitations = %+v, want the saved one", open)
	}
	n.mail.broken = false
	n.expect("owner", "POST", fmt.Sprintf("/api/orgs/{org}/invitations/%d/resend", open[0].ID), "", http.StatusOK)
	if len(n.mail.sent) != 1 {
		t.Errorf("emailed %d messages after resending, want 1", len(n.mail.sent))
	}
}

func TestRevokeInvitation(t *testing.T) {
	n := newNursery(t)
	ownerInvite := n.invite("owner", "boss@example.com", dbgen.OrgRoleOwner, http.StatusCreated)
	staffInvite := n.invite("owner", "helper@example.com", dbgen.OrgRoleStaff, http.StatusCreated)

	n.expect("admin", "DELETE", fmt.Sprintf("/api/orgs/{org}/invitations/%d", ownerInvite.ID), "", http.StatusForbidden)
	n.expect("admin", "DELETE", fmt.Sprintf("/api/orgs/{org}/invitations/%d", staffInvite.ID), "", http.StatusNoContent)
	n.expect("admin", "DELETE", fmt.Sprintf("/api/orgs/{org}/invitations/%d", staffInvite.ID), "", http.StatusNotFound) // already revoked
}

func TestAcceptInvitation(t *testing.T) {
	n := newNursery(t)
	invitation := n.invite("owner", "outsider@example.com", dbgen.OrgRoleStaff, http.StatusCreated)
	accept := fmt.Sprintf("/api/me/invitations/%d/accept", invitation.ID)

	// The invited person sees it in /api/me.
	var me meResponse
	_ = json.Unmarshal(n.expect("outsider", "GET", "/api/me", "", http.StatusOK).Body.Bytes(), &me)
	if len(me.PendingInvitations) != 1 || me.PendingInvitations[0].OrganizationName != "Test Nursery" {
		t.Fatalf("pending invitations = %+v, want the Test Nursery invitation", me.PendingInvitations)
	}

	n.expect("viewer", "POST", accept, "", http.StatusNotFound) // only the invited email can accept

	var membership membershipResponse
	_ = json.Unmarshal(n.expect("outsider", "POST", accept, "", http.StatusOK).Body.Bytes(), &membership)
	if membership.OrganizationID != n.org.ID || membership.Role != dbgen.OrgRoleStaff {
		t.Errorf("membership = %+v, want staff in Test Nursery", membership)
	}
	n.expect("outsider", "GET", "/api/orgs/{org}", "", http.StatusOK) // now a member
	n.expect("outsider", "POST", accept, "", http.StatusGone)         // can't accept twice
}

func TestExpiredInvitation(t *testing.T) {
	n := newNursery(t)
	invitation := n.invite("owner", "outsider@example.com", dbgen.OrgRoleStaff, http.StatusCreated)
	if _, err := n.db.Exec(t.Context(), "UPDATE invitations SET expires_at = now() - interval '1 day' WHERE id = $1", invitation.ID); err != nil {
		t.Fatal(err)
	}

	n.expect("outsider", "POST", fmt.Sprintf("/api/me/invitations/%d/accept", invitation.ID), "", http.StatusGone)
	n.invite("owner", "outsider@example.com", dbgen.OrgRoleStaff, http.StatusCreated) // an expired one doesn't block a new one
}

func TestDeclineInvitation(t *testing.T) {
	n := newNursery(t)
	invitation := n.invite("owner", "outsider@example.com", dbgen.OrgRoleStaff, http.StatusCreated)

	n.expect("outsider", "POST", fmt.Sprintf("/api/me/invitations/%d/decline", invitation.ID), "", http.StatusNoContent)

	var me meResponse
	_ = json.Unmarshal(n.expect("outsider", "GET", "/api/me", "", http.StatusOK).Body.Bytes(), &me)
	if len(me.PendingInvitations) != 0 {
		t.Errorf("pending invitations = %+v, want none after declining", me.PendingInvitations)
	}
}
