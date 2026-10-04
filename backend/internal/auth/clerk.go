package auth

import (
	"context"
	"net/http"

	"github.com/clerk/clerk-sdk-go/v2"
	clerkhttp "github.com/clerk/clerk-sdk-go/v2/http"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/gin-gonic/gin"
)

// sessionClaims are extra claims added to Clerk's session token in the Clerk dashboard
// (Configure → Sessions → Customize session token):
//
//	{"email": "{{user.primary_email_address}}", "name": "{{user.full_name}}"}
type sessionClaims struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// Clerk verifies the session token in the "Authorization: Bearer <token>" header against Clerk's
// signing keys (fetched with secretKey, then cached). Tokens must have been issued to one of
// allowedOrigins, the web app. Requests without a token continue unauthenticated, and
// RequireUser rejects them where sign-in is needed.
func Clerk(secretKey string, allowedOrigins []string) gin.HandlerFunc {
	keys := jwks.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})

	verify := clerkhttp.WithHeaderAuthorization(
		clerkhttp.JWKSClient(keys),
		clerkhttp.AuthorizedPartyMatches(allowedOrigins...),
		clerkhttp.CustomClaimsConstructor(func(context.Context) any { return &sessionClaims{} }),
		clerkhttp.AuthorizationFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid or expired session token"}`))
		})),
	)
	return fromHTTPMiddleware(verify)
}

// identityFromContext returns who the verified session token says the caller is.
func identityFromContext(ctx context.Context) (Identity, bool) {
	claims, ok := clerk.SessionClaimsFromContext(ctx)
	if !ok {
		return Identity{}, false
	}
	identity := Identity{Subject: claims.Subject}
	if custom, ok := claims.Custom.(*sessionClaims); ok {
		identity.Email = custom.Email
		identity.Name = custom.Name
	}
	return identity, true
}

// fromHTTPMiddleware lets Gin use standard net/http middleware, such as Clerk's.
func fromHTTPMiddleware(middleware func(http.Handler) http.Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		passed := false
		next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			passed = true
			c.Request = r // carries the context the middleware added (the verified claims)
			c.Next()
		})
		middleware(next).ServeHTTP(c.Writer, c.Request)
		if !passed {
			c.Abort() // the middleware rejected the request and already wrote the response
		}
	}
}
