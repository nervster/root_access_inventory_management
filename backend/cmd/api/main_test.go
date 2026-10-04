package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
)

// testRouter is the real router with authentication that never verifies anyone,
// and no database (these tests never reach it).
func testRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return newRouter(func(c *gin.Context) { c.Next() }, auth.NewStore(nil), organization.NewStore(nil), organization.Deliverer{})
}

func TestHealth(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	testRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if want := `{"status":"ok"}`; recorder.Body.String() != want {
		t.Fatalf("body = %s, want %s", recorder.Body.String(), want)
	}
}

func TestMeRequiresSignIn(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	testRouter().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
