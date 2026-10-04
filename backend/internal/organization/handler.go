package organization

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Routes adds the organization routes to signedIn, a group behind auth.RequireUser.
func Routes(signedIn *gin.RouterGroup, store *Store, deliverer Deliverer) {
	h := handler{store: store, deliverer: deliverer}
	signedIn.GET("/me", h.me)
	signedIn.POST("/me/invitations/:invitationId/accept", h.acceptInvitation)
	signedIn.POST("/me/invitations/:invitationId/decline", h.declineInvitation)

	org := signedIn.Group("/orgs/:orgId", RequireMember(store))
	org.GET("", RequirePermission(OrganizationView), h.getOrganization)
	org.PATCH("", RequirePermission(OrganizationManage), h.updateOrganization)
	org.GET("/members", RequirePermission(TeamView), h.listMembers)
	org.PATCH("/members/:memberId", RequirePermission(RolesManage), h.changeRole)
	org.DELETE("/members/:memberId", h.removeMember) // anyone may leave; removing others is checked inside

	// Inviting is limited further to roles the inviter may manage (CanManageRole).
	invitations := org.Group("/invitations", RequirePermission(TeamManage))
	invitations.GET("", h.listInvitations)
	invitations.POST("", h.createInvitation)
	invitations.POST("/:invitationId/resend", h.resendInvitation)
	invitations.DELETE("/:invitationId", h.revokeInvitation)
}

type handler struct {
	store          *Store
	deliverer      Deliverer
	platformAdmins auth.PlatformAdmins // only used by the platform routes
}

// --- GET /api/me ---

type meResponse struct {
	ID                 int64                       `json:"id"`
	Email              string                      `json:"email"`
	DisplayName        *string                     `json:"displayName"`
	IsPlatformAdmin    bool                        `json:"isPlatformAdmin"`
	Memberships        []membershipResponse        `json:"memberships"`
	PendingInvitations []pendingInvitationResponse `json:"pendingInvitations"`
}

// membershipResponse includes the permissions the role grants, so the web app can show or hide actions.
type membershipResponse struct {
	OrganizationID   int64                    `json:"organizationId"`
	OrganizationName string                   `json:"organizationName"`
	Slug             string                   `json:"slug"`
	Status           dbgen.OrganizationStatus `json:"status"`
	Role             dbgen.OrgRole            `json:"role"`
	Permissions      []Permission             `json:"permissions"`
}

func (h handler) me(c *gin.Context) {
	user := auth.CurrentUser(c)
	memberships, err := h.memberships(c, user.ID)
	if err != nil {
		failInternal(c, err)
		return
	}
	rows, err := h.store.PendingInvitationsFor(c.Request.Context(), user.Email)
	if err != nil {
		failInternal(c, err)
		return
	}

	invitations := make([]pendingInvitationResponse, len(rows))
	for i, row := range rows {
		invitations[i] = pendingInvitationResponse(row)
	}
	c.JSON(http.StatusOK, meResponse{
		ID: user.ID, Email: user.Email, DisplayName: user.DisplayName, IsPlatformAdmin: auth.IsPlatformAdmin(c),
		Memberships: memberships, PendingInvitations: invitations,
	})
}

func (h handler) memberships(c *gin.Context, userID int64) ([]membershipResponse, error) {
	rows, err := h.store.MembershipsForUser(c.Request.Context(), userID)
	if err != nil {
		return nil, err
	}
	memberships := make([]membershipResponse, len(rows))
	for i, row := range rows {
		memberships[i] = membershipResponse{
			OrganizationID:   row.OrganizationID,
			OrganizationName: row.OrganizationName,
			Slug:             row.Slug,
			Status:           row.Status,
			Role:             row.Role,
			Permissions:      PermissionsFor(row.Role),
		}
	}
	return memberships, nil
}

// --- GET and PATCH /api/orgs/:orgId ---

type organizationResponse struct {
	ID       int64                    `json:"id"`
	Name     string                   `json:"name"`
	Slug     string                   `json:"slug"`
	TimeZone string                   `json:"timeZone"`
	Status   dbgen.OrganizationStatus `json:"status"`
}

func newOrganizationResponse(org dbgen.Organization) organizationResponse {
	return organizationResponse{ID: org.ID, Name: org.Name, Slug: org.Slug, TimeZone: org.TimeZone, Status: org.Status}
}

func (h handler) getOrganization(c *gin.Context) {
	org, err := h.store.Organization(c.Request.Context(), CurrentAccess(c).OrganizationID)
	if err != nil {
		failInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, newOrganizationResponse(org))
}

// updateOrganizationRequest: leave a field out to keep its current value.
type updateOrganizationRequest struct {
	Name     *string `json:"name"`
	TimeZone *string `json:"timeZone"` // IANA name, e.g. America/Chicago
}

func (h handler) updateOrganization(c *gin.Context) {
	var req updateOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > 200 {
			fail(c, http.StatusBadRequest, "name must be 1 to 200 characters")
			return
		}
		req.Name = &name
	}
	if req.TimeZone != nil {
		if _, err := time.LoadLocation(*req.TimeZone); err != nil || *req.TimeZone == "" {
			fail(c, http.StatusBadRequest, "unknown time zone; use an IANA name like America/Chicago")
			return
		}
	}

	org, err := h.store.UpdateOrganization(c.Request.Context(), CurrentAccess(c).OrganizationID, req.Name, req.TimeZone)
	if err != nil {
		failInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, newOrganizationResponse(org))
}

// --- /api/orgs/:orgId/members ---

type memberResponse struct {
	ID          int64         `json:"id"`
	UserID      int64         `json:"userId"`
	Email       string        `json:"email"`
	DisplayName *string       `json:"displayName"`
	Role        dbgen.OrgRole `json:"role"`
	JoinedAt    time.Time     `json:"joinedAt"`
}

func newMemberResponse(m Member) memberResponse {
	return memberResponse{ID: m.ID, UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Role: m.Role, JoinedAt: m.CreatedAt}
}

func (h handler) listMembers(c *gin.Context) {
	members, err := h.store.Members(c.Request.Context(), CurrentAccess(c).OrganizationID)
	if err != nil {
		failInternal(c, err)
		return
	}
	response := make([]memberResponse, len(members))
	for i, m := range members {
		response[i] = newMemberResponse(m)
	}
	c.JSON(http.StatusOK, response)
}

type changeRoleRequest struct {
	Role dbgen.OrgRole `json:"role"`
}

func (h handler) changeRole(c *gin.Context) {
	memberID, ok := idParam(c, "memberId")
	if !ok {
		fail(c, http.StatusNotFound, "member not found")
		return
	}
	var req changeRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil || !validRole(req.Role) {
		fail(c, http.StatusBadRequest, "role must be owner, admin, staff, or viewer")
		return
	}

	member, err := h.store.ChangeRole(c.Request.Context(), CurrentAccess(c).OrganizationID, memberID, req.Role)
	if err != nil {
		failTeamChange(c, err)
		return
	}
	c.JSON(http.StatusOK, newMemberResponse(member))
}

// removeMember lets anyone leave. Removing someone else needs team.manage and a role the actor may manage.
func (h handler) removeMember(c *gin.Context) {
	access := CurrentAccess(c)
	memberID, ok := idParam(c, "memberId")
	if !ok {
		fail(c, http.StatusNotFound, "member not found")
		return
	}

	member, err := h.store.Member(c.Request.Context(), access.OrganizationID, memberID)
	if err != nil {
		failTeamChange(c, err)
		return
	}
	isSelf := member.ID == access.MembershipID
	if !isSelf && !(Has(access.Role, TeamManage) && CanManageRole(access.Role, member.Role)) {
		fail(c, http.StatusForbidden, "you can't remove this member")
		return
	}

	if err := h.store.RemoveMember(c.Request.Context(), access.OrganizationID, memberID); err != nil {
		failTeamChange(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- helpers ---

// idParam reads a numeric ID from the route, e.g. :orgId.
func idParam(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	return id, err == nil && id > 0
}

func fail(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}

// failInternal logs an unexpected error and hides its details from the client.
func failInternal(c *gin.Context, err error) {
	slog.Error("request failed", "method", c.Request.Method, "path", c.FullPath(), "error", err)
	fail(c, http.StatusInternalServerError, "something went wrong")
}

func failTeamChange(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		fail(c, http.StatusNotFound, "member not found")
	case errors.Is(err, ErrLastOwner):
		fail(c, http.StatusConflict, "an organization must keep at least one Owner; make someone else an Owner first")
	default:
		failInternal(c, err)
	}
}
