package organization

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

type invitationResponse struct {
	ID        int64         `json:"id"`
	Email     string        `json:"email"`
	Role      dbgen.OrgRole `json:"role"`
	CreatedAt time.Time     `json:"createdAt"`
	ExpiresAt time.Time     `json:"expiresAt"`
	Expired   bool          `json:"expired"`
}

func newInvitationResponse(i dbgen.Invitation) invitationResponse {
	return invitationResponse{
		ID: i.ID, Email: i.Email, Role: i.Role, CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
		Expired: !i.ExpiresAt.After(time.Now()),
	}
}

// pendingInvitationResponse is an invitation addressed to the signed-in user (in GET /api/me).
type pendingInvitationResponse struct {
	ID               int64         `json:"id"`
	OrganizationID   int64         `json:"organizationId"`
	OrganizationName string        `json:"organizationName"`
	Role             dbgen.OrgRole `json:"role"`
	ExpiresAt        time.Time     `json:"expiresAt"`
}

// --- /api/orgs/:orgId/invitations (team.manage) ---

func (h handler) listInvitations(c *gin.Context) {
	invitations, err := h.store.Invitations(c.Request.Context(), CurrentAccess(c).OrganizationID)
	if err != nil {
		failInternal(c, err)
		return
	}
	response := make([]invitationResponse, len(invitations))
	for i, invitation := range invitations {
		response[i] = newInvitationResponse(invitation)
	}
	c.JSON(http.StatusOK, response)
}

type createInvitationRequest struct {
	Email string        `json:"email"`
	Role  dbgen.OrgRole `json:"role"`
}

func (h handler) createInvitation(c *gin.Context) {
	access := CurrentAccess(c)
	var req createInvitationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	// A plain address only (not "Name <address>").
	address, err := mail.ParseAddress(req.Email)
	if err != nil || address.Address != strings.TrimSpace(req.Email) || len(address.Address) > 320 {
		fail(c, http.StatusBadRequest, "enter a valid email address")
		return
	}
	if !validRole(req.Role) {
		fail(c, http.StatusBadRequest, "role must be owner, admin, staff, or viewer")
		return
	}
	if !CanManageRole(access.Role, req.Role) {
		fail(c, http.StatusForbidden, "you can't invite someone as "+string(req.Role))
		return
	}

	user := auth.CurrentUser(c)
	invitation, err := h.store.CreateInvitation(c.Request.Context(), access.OrganizationID, address.Address, req.Role, &user.ID)
	switch {
	case errors.Is(err, ErrAlreadyMember):
		fail(c, http.StatusConflict, address.Address+" is already a member")
		return
	case errors.Is(err, ErrAlreadyInvited):
		fail(c, http.StatusConflict, address.Address+" already has a pending invitation; resend it instead")
		return
	case err != nil:
		failInternal(c, err)
		return
	}

	if !h.deliver(c, invitation) {
		return
	}
	c.JSON(http.StatusCreated, newInvitationResponse(invitation))
}

func (h handler) resendInvitation(c *gin.Context) {
	invitation, ok := h.manageableInvitation(c)
	if !ok {
		return
	}
	invitation, err := h.store.ExtendInvitation(c.Request.Context(), invitation.OrganizationID, invitation.ID)
	if err != nil {
		failInternal(c, err)
		return
	}
	if !h.deliver(c, invitation) {
		return
	}
	c.JSON(http.StatusOK, newInvitationResponse(invitation))
}

func (h handler) revokeInvitation(c *gin.Context) {
	invitation, ok := h.manageableInvitation(c)
	if !ok {
		return
	}
	if err := h.store.RevokeInvitation(c.Request.Context(), invitation.OrganizationID, invitation.ID); err != nil {
		failInternal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// manageableInvitation loads the open invitation in the route, if the signed-in member may manage
// its role. Otherwise it writes the error response and returns false.
func (h handler) manageableInvitation(c *gin.Context) (dbgen.Invitation, bool) {
	access := CurrentAccess(c)
	invitationID, ok := idParam(c, "invitationId")
	if !ok {
		fail(c, http.StatusNotFound, "invitation not found")
		return dbgen.Invitation{}, false
	}
	invitation, err := h.store.OpenInvitation(c.Request.Context(), access.OrganizationID, invitationID)
	switch {
	case errors.Is(err, ErrNotFound):
		fail(c, http.StatusNotFound, "invitation not found")
		return dbgen.Invitation{}, false
	case err != nil:
		failInternal(c, err)
		return dbgen.Invitation{}, false
	case !CanManageRole(access.Role, invitation.Role):
		fail(c, http.StatusForbidden, "you can't manage this invitation")
		return dbgen.Invitation{}, false
	}
	return invitation, true
}

// deliver sends the invitation. The invitation is already saved, so if sending fails the response
// says so (502) and the inviter can resend. Returns false when it wrote an error response.
func (h handler) deliver(c *gin.Context, invitation dbgen.Invitation) bool {
	org, err := h.store.Organization(c.Request.Context(), invitation.OrganizationID)
	if err != nil {
		failInternal(c, err)
		return false
	}
	if err := h.deliverer.Deliver(c.Request.Context(), invitation, org.Name); err != nil {
		slog.Error("deliver invitation", "invitation", invitation.ID, "error", err)
		fail(c, http.StatusBadGateway, "the invitation was saved, but it couldn't be sent; try resending it")
		return false
	}
	return true
}

// --- /api/me/invitations/:invitationId (the invited person) ---

func (h handler) acceptInvitation(c *gin.Context) {
	user := auth.CurrentUser(c)
	invitationID, ok := idParam(c, "invitationId")
	if !ok {
		fail(c, http.StatusNotFound, "invitation not found")
		return
	}

	orgID, err := h.store.AcceptInvitation(c.Request.Context(), invitationID, user)
	switch {
	case errors.Is(err, ErrNotFound):
		fail(c, http.StatusNotFound, "invitation not found")
		return
	case errors.Is(err, ErrInvitationGone):
		fail(c, http.StatusGone, "this invitation is no longer valid; ask the organization to send a new one")
		return
	case errors.Is(err, ErrSuspended):
		fail(c, http.StatusForbidden, "organization is suspended")
		return
	case err != nil:
		failInternal(c, err)
		return
	}

	memberships, err := h.memberships(c, user.ID)
	if err != nil {
		failInternal(c, err)
		return
	}
	for _, m := range memberships {
		if m.OrganizationID == orgID {
			c.JSON(http.StatusOK, m)
			return
		}
	}
	failInternal(c, errors.New("accepted invitation but membership not found"))
}

func (h handler) declineInvitation(c *gin.Context) {
	invitationID, ok := idParam(c, "invitationId")
	if !ok {
		fail(c, http.StatusNotFound, "invitation not found")
		return
	}
	err := h.store.DeclineInvitation(c.Request.Context(), invitationID, auth.CurrentUser(c))
	switch {
	case errors.Is(err, ErrNotFound):
		fail(c, http.StatusNotFound, "invitation not found")
	case err != nil:
		failInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}
