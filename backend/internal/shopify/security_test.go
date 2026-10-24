package shopify

import (
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestWebhookStoreMustMatchItsBody(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	// A genuine (signed) uninstall from another store, replayed with this store's domain in the header.
	if code := s.webhook("app/uninstalled", testShop, `{"myshopify_domain": "other-store.myshopify.com"}`, true); code != 401 {
		t.Errorf("mismatched store: %d, want 401", code)
	}
	if s.connection("owner").NeedsReconnect {
		t.Error("the nursery was disconnected by a webhook for another store")
	}
	if code := s.webhook("customers/redact", testShop, `{"orders_to_redact": [1]}`, true); code != 401 {
		t.Errorf("privacy webhook naming no store: %d, want 401", code)
	}
	// Order webhooks only queue an import: no body check.
	if code := s.webhook("orders/updated", testShop, `{"id": 1}`, true); code != 200 {
		t.Errorf("order webhook: %d, want 200", code)
	}
}

func TestProxySignatureExpires(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	stale := url.Values{"timestamp": {strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)}}
	if code := s.proxyGet("/pickup-slots", testShop, "test-secret", stale).Code; code != 401 {
		t.Errorf("signed two hours ago: %d, want 401", code)
	}
}

func TestClientIPIsTheShoppers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	// Caddy's own X-Forwarded-For is Shopify's address; Shopify's (kept by Caddy) is the shopper's.
	c.Request.Header.Set("X-Forwarded-For", "23.227.38.1")
	c.Request.Header.Set("X-Shopify-Forwarded-For", "198.51.100.7")
	if got := clientIP(c); got != "198.51.100.7" {
		t.Errorf("client IP = %s, want the shopper's 198.51.100.7", got)
	}
}
