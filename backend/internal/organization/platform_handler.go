package organization

import (
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// PlatformRoutes adds the platform admin routes to signedIn, a group behind auth.RequireUser.
// Platform admins manage nurseries' accounts (create, suspend, fix members and invitations);
// business data stays visible only to members.
func PlatformRoutes(signedIn *gin.RouterGroup, store *Store, deliverer Deliverer, admins auth.PlatformAdmins) {
	h := handler{store: store, deliverer: deliverer, platformAdmins: admins}
	platform := signedIn.Group("/platform", auth.RequirePlatformAdmin)
	platform.GET("/organizations", h.platformListOrganizations)
	platform.POST("/organizations", h.platformCreateOrganization)
	platform.GET("/users", h.platformSearchUsers)

	// The member and invitation routes reuse the nursery's own handlers, acting as an Owner, so
	// the same rules apply (e.g. a nursery always keeps an Owner).
	org := platform.Group("/organizations/:orgId", h.actAsOwner)
	org.GET("", h.platformGetOrganization)
	org.PATCH("", h.platformUpdateOrganization)
	org.PATCH("/members/:memberId", h.changeRole)
	org.DELETE("/members/:memberId", h.removeMember)
	org.POST("/invitations", h.createInvitation)
	org.POST("/invitations/:invitationId/resend", h.resendInvitation)
	org.DELETE("/invitations/:invitationId", h.revokeInvitation)
}

// actAsOwner gives a platform admin Owner access to the organization in the route, for account
// management only (see PlatformRoutes). They aren't a member, so MembershipID is 0.
func (h handler) actAsOwner(c *gin.Context) {
	orgID, ok := idParam(c, "orgId")
	if !ok {
		fail(c, http.StatusNotFound, "organization not found")
		return
	}
	if _, err := h.store.OrganizationSummary(c.Request.Context(), orgID); err != nil {
		if errors.Is(err, ErrNotFound) {
			fail(c, http.StatusNotFound, "organization not found")
		} else {
			failInternal(c, err)
		}
		return
	}
	c.Set(accessKey, Access{OrganizationID: orgID, Role: dbgen.OrgRoleOwner})
	c.Next()
}

type platformOrganizationResponse struct {
	ID          int64                    `json:"id"`
	Name        string                   `json:"name"`
	Slug        string                   `json:"slug"`
	TimeZone    string                   `json:"timeZone"`
	Status      dbgen.OrganizationStatus `json:"status"`
	CreatedAt   time.Time                `json:"createdAt"`
	MemberCount int64                    `json:"memberCount"`
}

func (h handler) platformListOrganizations(c *gin.Context) {
	orgs, err := h.store.AllOrganizations(c.Request.Context())
	if err != nil {
		failInternal(c, err)
		return
	}
	response := make([]platformOrganizationResponse, len(orgs))
	for i, org := range orgs {
		response[i] = platformOrganizationResponse(org)
	}
	c.JSON(http.StatusOK, response)
}

type createOrganizationRequest struct {
	Name       string `json:"name"`
	Slug       string `json:"slug"`     // optional; made from the name when empty
	TimeZone   string `json:"timeZone"` // optional; America/Chicago when empty
	OwnerEmail string `json:"ownerEmail"`
}

type createOrganizationResponse struct {
	Organization    platformOrganizationResponse `json:"organization"`
	OwnerInvitation invitationResponse           `json:"ownerInvitation"`
	DeliveryError   *string                      `json:"deliveryError"` // set when the invitation couldn't be sent
}

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
var nonSlugCharacters = regexp.MustCompile(`[^a-z0-9]+`)

// platformCreateOrganization creates a nursery and invites its first Owner.
func (h handler) platformCreateOrganization(c *gin.Context) {
	var req createOrganizationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		fail(c, http.StatusBadRequest, "name must be 1 to 200 characters")
		return
	}
	slug := req.Slug
	if slug == "" {
		slug = strings.Trim(nonSlugCharacters.ReplaceAllString(strings.ToLower(name), "-"), "-")
	}
	if !slugPattern.MatchString(slug) || len(slug) > 100 {
		fail(c, http.StatusBadRequest, "slug must be lowercase letters and numbers separated by single hyphens, e.g. root-access-htx")
		return
	}
	timeZone := req.TimeZone
	if timeZone == "" {
		timeZone = "America/Chicago"
	}
	if _, err := time.LoadLocation(timeZone); err != nil {
		fail(c, http.StatusBadRequest, "unknown time zone; use an IANA name like America/Chicago")
		return
	}
	owner, err := mail.ParseAddress(req.OwnerEmail)
	if err != nil || owner.Address != strings.TrimSpace(req.OwnerEmail) {
		fail(c, http.StatusBadRequest, "enter a valid owner email address")
		return
	}

	org, invitation, err := h.store.CreateOrganization(c.Request.Context(), name, slug, timeZone, owner.Address, auth.CurrentUser(c).ID)
	switch {
	case errors.Is(err, ErrSlugTaken):
		fail(c, http.StatusConflict, "an organization with slug "+slug+" already exists")
		return
	case err != nil:
		failInternal(c, err)
		return
	}

	// The nursery exists either way; report a failed invitation instead of failing the request.
	response := createOrganizationResponse{
		Organization: platformOrganizationResponse{
			ID: org.ID, Name: org.Name, Slug: org.Slug, TimeZone: org.TimeZone, Status: org.Status, CreatedAt: org.CreatedAt,
		},
		OwnerInvitation: newInvitationResponse(invitation),
	}
	if err := h.deliverer.Deliver(c.Request.Context(), invitation, org.Name); err != nil {
		slog.Error("deliver owner invitation", "invitation", invitation.ID, "error", err)
		message := "the Owner invitation couldn't be sent; resend it from the organization's page"
		response.DeliveryError = &message
	}
	c.JSON(http.StatusCreated, response)
}

type platformOrganizationDetailResponse struct {
	Organization       platformOrganizationResponse `json:"organization"`
	Members            []memberResponse             `json:"members"`
	PendingInvitations []invitationResponse         `json:"pendingInvitations"`
}

func (h handler) platformGetOrganization(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := CurrentAccess(c).OrganizationID

	summary, err := h.store.OrganizationSummary(ctx, orgID)
	if err != nil {
		failInternal(c, err)
		return
	}
	members, err := h.store.Members(ctx, orgID)
	if err != nil {
		failInternal(c, err)
		return
	}
	invitations, err := h.store.Invitations(ctx, orgID)
	if err != nil {
		failInternal(c, err)
		return
	}

	response := platformOrganizationDetailResponse{
		Organization:       platformOrganizationResponse(summary),
		Members:            make([]memberResponse, len(members)),
		PendingInvitations: make([]invitationResponse, len(invitations)),
	}
	for i, m := range members {
		response.Members[i] = newMemberResponse(m)
	}
	for i, invitation := range invitations {
		response.PendingInvitations[i] = newInvitationResponse(invitation)
	}
	c.JSON(http.StatusOK, response)
}

type platformUpdateOrganizationRequest struct {
	Name   *string                   `json:"name"`
	Status *dbgen.OrganizationStatus `json:"status"` // "active" or "suspended"
}

func (h handler) platformUpdateOrganization(c *gin.Context) {
	orgID := CurrentAccess(c).OrganizationID
	var req platformUpdateOrganizationRequest
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
	if req.Status != nil && *req.Status != dbgen.OrganizationStatusActive && *req.Status != dbgen.OrganizationStatusSuspended {
		fail(c, http.StatusBadRequest, "status must be active or suspended")
		return
	}

	if err := h.store.UpdateOrganizationAccount(c.Request.Context(), orgID, req.Name, req.Status); err != nil {
		failInternal(c, err)
		return
	}
	summary, err := h.store.OrganizationSummary(c.Request.Context(), orgID)
	if err != nil {
		failInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, platformOrganizationResponse(summary))
}

type platformUserResponse struct {
	ID              int64                        `json:"id"`
	Email           string                       `json:"email"`
	DisplayName     *string                      `json:"displayName"`
	IsPlatformAdmin bool                         `json:"isPlatformAdmin"`
	CreatedAt       time.Time                    `json:"createdAt"`
	Memberships     []platformMembershipResponse `json:"memberships"`
}

type platformMembershipResponse struct {
	OrganizationID   int64         `json:"organizationId"`
	OrganizationName string        `json:"organizationName"`
	Role             dbgen.OrgRole `json:"role"`
}

// platformSearchUsers handles GET /api/platform/users?email=…, for support:
// "which nurseries is this person in?"
func (h handler) platformSearchUsers(c *gin.Context) {
	users, err := h.store.SearchUsers(c.Request.Context(), c.Query("email"))
	if err != nil {
		failInternal(c, err)
		return
	}
	response := make([]platformUserResponse, len(users))
	for i, u := range users {
		memberships := make([]platformMembershipResponse, len(u.Memberships))
		for j, m := range u.Memberships {
			memberships[j] = platformMembershipResponse{OrganizationID: m.OrganizationID, OrganizationName: m.OrganizationName, Role: m.Role}
		}
		response[i] = platformUserResponse{
			ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, IsPlatformAdmin: h.platformAdmins[u.Email],
			CreatedAt: u.CreatedAt, Memberships: memberships,
		}
	}
	c.JSON(http.StatusOK, response)
}
