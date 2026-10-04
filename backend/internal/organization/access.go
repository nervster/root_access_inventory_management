package organization

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
)

const accessKey = "organization.access"

// RequireMember resolves the organization in the route (:orgId) for the signed-in user and makes
// it the tenant for the rest of the request. Non-members get 404, so organization IDs can't be
// probed; members of a suspended organization get 403. Must run after auth.RequireUser.
func RequireMember(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		orgID, ok := idParam(c, "orgId")
		if !ok {
			fail(c, http.StatusNotFound, "organization not found")
			return
		}

		access, err := store.Access(c.Request.Context(), orgID, auth.CurrentUser(c).ID)
		switch {
		case errors.Is(err, ErrNotFound):
			fail(c, http.StatusNotFound, "organization not found")
			return
		case err != nil:
			failInternal(c, err)
			return
		case access.Suspended:
			fail(c, http.StatusForbidden, "organization is suspended")
			return
		}

		c.Set(accessKey, access)
		c.Next()
	}
}

// RequirePermission rejects (403) members whose role doesn't grant permission.
// Must run after RequireMember.
func RequirePermission(permission Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !Has(CurrentAccess(c).Role, permission) {
			fail(c, http.StatusForbidden, "you don't have permission to do that")
			return
		}
		c.Next()
	}
}

// CurrentAccess returns the signed-in user's access to the request's organization.
// Only call it in handlers behind RequireMember.
func CurrentAccess(c *gin.Context) Access {
	return c.MustGet(accessKey).(Access)
}
