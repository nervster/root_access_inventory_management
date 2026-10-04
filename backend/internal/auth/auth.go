// Package auth signs people in: Clerk proves who they are, and this package keeps a local user
// record for them. What they may do inside a nursery is decided elsewhere, from memberships.
package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Identity is who a verified session token says the caller is.
type Identity struct {
	Subject string // Clerk user ID
	Email   string
	Name    string
}

const (
	userKey          = "auth.user"
	platformAdminKey = "auth.platformAdmin"
)

// PlatformAdmins is the set of emails allowed to run the platform: create and suspend nurseries,
// fix their memberships, and look up users. It's set in config, not in the database.
type PlatformAdmins map[string]bool

func NewPlatformAdmins(emails []string) PlatformAdmins {
	admins := PlatformAdmins{}
	for _, email := range emails {
		if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
			admins[email] = true
		}
	}
	return admins
}

// RequireUser rejects requests without a verified session (401) and loads the signed-in user,
// creating them on first sign-in. Handlers after it read the user with CurrentUser.
func RequireUser(users *Store, admins PlatformAdmins) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := identityFromContext(c.Request.Context())
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "sign in required"})
			return
		}
		if identity.Email == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": `session token has no "email" claim; add it in the Clerk dashboard (Sessions → Customize session token)`,
			})
			return
		}

		user, err := users.SyncUser(c.Request.Context(), identity)
		if err != nil {
			slog.Error("sync signed-in user", "error", err)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "something went wrong"})
			return
		}

		SetCurrentUser(c, user, admins[user.Email])
		c.Next()
	}
}

// SetCurrentUser marks user as signed in for this request. RequireUser calls it; tests can too.
func SetCurrentUser(c *gin.Context, user dbgen.User, platformAdmin bool) {
	c.Set(userKey, user)
	c.Set(platformAdminKey, platformAdmin)
}

// IsPlatformAdmin reports whether the signed-in user runs the platform.
func IsPlatformAdmin(c *gin.Context) bool {
	return c.GetBool(platformAdminKey)
}

// RequirePlatformAdmin rejects (403) anyone who isn't a platform admin. Must run after RequireUser.
func RequirePlatformAdmin(c *gin.Context) {
	if !IsPlatformAdmin(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "platform admins only"})
		return
	}
	c.Next()
}

// CurrentUser returns the signed-in user. Only call it in handlers behind RequireUser.
func CurrentUser(c *gin.Context) dbgen.User {
	return c.MustGet(userKey).(dbgen.User)
}
