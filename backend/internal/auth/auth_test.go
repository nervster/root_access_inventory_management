package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// signedInAs stands in for the Clerk middleware: it marks the request as carrying a verified
// session token for identity, or leaves it unauthenticated when identity is nil.
func signedInAs(identity *Identity) gin.HandlerFunc {
	return func(c *gin.Context) {
		if identity != nil {
			claims := &clerk.SessionClaims{
				RegisteredClaims: clerk.RegisteredClaims{Subject: identity.Subject},
				Custom:           &sessionClaims{Email: identity.Email, Name: identity.Name},
			}
			c.Request = c.Request.WithContext(clerk.ContextWithSessionClaims(c.Request.Context(), claims))
		}
		c.Next()
	}
}

func TestRequireUser(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		identity   *Identity
		wantStatus int
		wantBody   string
	}{
		{"not signed in", nil, http.StatusUnauthorized, ""},
		{"token without email claim", &Identity{Subject: "user_no_email"}, http.StatusUnauthorized, ""},
		{"signed in", &Identity{Subject: "user_me", Email: "Me@Example.com"}, http.StatusOK, "me@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/", signedInAs(tt.identity), RequireUser(NewStore(dbtest.Tx(t))), func(c *gin.Context) {
				c.String(http.StatusOK, CurrentUser(c).Email)
			})

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", recorder.Code, tt.wantStatus, recorder.Body)
			}
			if tt.wantBody != "" && recorder.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}
