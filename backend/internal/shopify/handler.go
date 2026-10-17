package shopify

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

const stateCookie = "shopify_state"
const callbackPath = "/api/shopify/callback"

// Routes adds the Shopify routes: the callback to api (Shopify sends the browser there, with no
// session token) and the Owner's settings to signedIn.
func Routes(api, signedIn *gin.RouterGroup, orgs *organization.Store, store *Store, appURL string) {
	h := handler{orgs: orgs, store: store, appURL: strings.TrimSuffix(appURL, "/")}
	api.GET(strings.TrimPrefix(callbackPath, "/api"), h.callback)
	api.POST("/shopify/webhooks", h.webhook)

	shopify := signedIn.Group("/orgs/:orgId/shopify",
		organization.RequireMember(orgs), organization.RequirePermission(organization.IntegrationsManage))
	shopify.GET("", h.getConnection)
	shopify.POST("/connect", h.connect)
	shopify.DELETE("", h.disconnect)
	shopify.GET("/locations", h.listLocations)
	shopify.PUT("/location", h.setLocation)
	shopify.POST("/sync", h.syncAll)
	shopify.GET("/products", h.listProducts)
	shopify.POST("/check", h.checkDrift)

	// Orders: any member who sees inventory can see whether they're arriving, and check now.
	orders := signedIn.Group("/orgs/:orgId/shopify/orders", organization.RequireMember(orgs))
	orders.GET("/status", organization.RequirePermission(organization.InventoryView), h.ordersStatus)
	orders.POST("/import", organization.RequirePermission(organization.InventoryEdit), h.importOrders)
}

type handler struct {
	orgs   *organization.Store
	store  *Store
	appURL string
}

type connectionResponse struct {
	Configured      bool       `json:"configured"`      // the server has the app's credentials
	OrdersCheckedAt *time.Time `json:"ordersCheckedAt"` // orders are read up to here
	OrdersError     *string    `json:"ordersError"`     // why reading orders failed last time
	MissingScopes   []string   `json:"missingScopes"`   // permissions added since it connected: connect again to grant them
	DriftCheckedAt  *time.Time `json:"driftCheckedAt"`  // the last nightly check
	DriftFound      []string   `json:"driftFound"`      // what it found different (and fixed), in words
	Connected       bool       `json:"connected"`
	Shop            *string    `json:"shop"`
	NeedsReconnect  bool       `json:"needsReconnect"`
	LocationID      *string    `json:"locationId"`
	LocationName    *string    `json:"locationName"`
	ConnectedByName *string    `json:"connectedByName"`
	ConnectedAt     *time.Time `json:"connectedAt"`
}

func (h handler) getConnection(c *gin.Context) {
	connection, err := h.store.Connection(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	response := connectionResponse{Configured: h.store.Configured()}
	switch {
	case errors.Is(err, ErrNotConnected):
	case err != nil:
		web.FailInternal(c, err)
		return
	default:
		response.Connected = true
		response.Shop = &connection.Shop
		response.NeedsReconnect = connection.NeedsReconnect
		response.LocationID, response.LocationName = connection.LocationID, connection.LocationName
		response.ConnectedByName, response.ConnectedAt = connection.ConnectedByName, &connection.ConnectedAt
		response.OrdersCheckedAt, response.OrdersError = connection.OrdersCheckedThrough, connection.OrdersError
		response.MissingScopes = Token{Scopes: strings.Split(connection.Scopes, ",")}.MissingScopes()
		response.DriftCheckedAt = connection.DriftCheckedAt
		if err := json.Unmarshal(connection.DriftDifferences, &response.DriftFound); err != nil {
			web.FailInternal(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, response)
}

// connect handles POST /orgs/:orgId/shopify/connect {shop}: it returns the Shopify page to send the
// Owner to, and sets a cookie so only this browser can finish connecting.
func (h handler) connect(c *gin.Context) {
	var req struct {
		Shop string `json:"shop"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	shop := NormalizeShop(req.Shop)
	if shop == "" {
		web.Fail(c, http.StatusBadRequest, "enter your store's address, like your-store.myshopify.com")
		return
	}

	authorizeURL, state, err := h.store.StartConnect(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, shop)
	switch {
	case errors.Is(err, ErrNotConfigured):
		web.Fail(c, http.StatusServiceUnavailable, "Shopify isn't set up on this server yet")
		return
	case errors.Is(err, ErrConnectedToOther):
		web.Fail(c, http.StatusConflict, "this nursery is connected to a different store; disconnect it first")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode) // sent when Shopify redirects back (a top-level GET)
	c.SetCookie(stateCookie, state, int(stateLifetime.Seconds()), callbackPath, "", strings.HasPrefix(h.appURL, "https://"), true)
	c.JSON(http.StatusOK, gin.H{"authorizeUrl": authorizeURL})
}

// callback handles GET /shopify/callback: Shopify sends the Owner here after they approve the app.
// It ends on the nursery's Settings page, with ?shopify=connected or ?shopify=<what went wrong>.
func (h handler) callback(c *gin.Context) {
	query := c.Request.URL.Query()
	cookie, _ := c.Cookie(stateCookie)
	c.SetCookie(stateCookie, "", -1, callbackPath, "", strings.HasPrefix(h.appURL, "https://"), true)

	// Until the attempt is found, the nursery isn't known, so problems get a plain page.
	state := query.Get("state")
	if !h.store.Configured() || !h.store.app.VerifyCallback(query) || state == "" ||
		subtle.ConstantTimeCompare([]byte(state), []byte(cookie)) != 1 {
		c.String(http.StatusBadRequest, "This Shopify link isn't valid here. Go back to Plant Stock → Settings and click Connect again.")
		return
	}
	attempt, err := h.store.TakeState(c.Request.Context(), state)
	switch {
	case errors.Is(err, ErrBadState):
		c.String(http.StatusBadRequest, "This Shopify link was already used. Go back to Plant Stock → Settings and click Connect again.")
		return
	case errors.Is(err, ErrExpired):
		h.backToSettings(c, attempt.OrganizationID, "expired")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	if query.Get("shop") != attempt.Shop {
		h.backToSettings(c, attempt.OrganizationID, "different-store")
		return
	}
	// They may have lost the permission (or the nursery been suspended) since clicking Connect.
	access, err := h.orgs.Access(c.Request.Context(), attempt.OrganizationID, attempt.UserID)
	if err != nil || access.Suspended || !organization.Has(access.Role, organization.IntegrationsManage) {
		h.backToSettings(c, attempt.OrganizationID, "not-allowed")
		return
	}

	err = h.store.FinishConnect(c.Request.Context(), attempt, query.Get("code"))
	switch {
	case errors.Is(err, ErrMissingScopes):
		h.backToSettings(c, attempt.OrganizationID, "missing-permissions")
	case errors.Is(err, ErrOtherNursery):
		h.backToSettings(c, attempt.OrganizationID, "other-nursery")
	case err != nil:
		slog.Error("shopify connect failed", "organization", attempt.OrganizationID, "shop", attempt.Shop, "error", err)
		h.backToSettings(c, attempt.OrganizationID, "failed")
	default:
		h.backToSettings(c, attempt.OrganizationID, "connected")
	}
}

func (h handler) backToSettings(c *gin.Context, orgID int64, result string) {
	c.Redirect(http.StatusFound, fmt.Sprintf("%s/orgs/%d/settings?shopify=%s", h.appURL, orgID, url.QueryEscape(result)))
}

func (h handler) disconnect(c *gin.Context) {
	switch err := h.store.Disconnect(c.Request.Context(), organization.CurrentAccess(c).OrganizationID); {
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "no store is connected")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

func (h handler) listLocations(c *gin.Context) {
	locations, err := h.store.Locations(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if failShopify(c, err) {
		return
	}
	c.JSON(http.StatusOK, locations)
}

func (h handler) setLocation(c *gin.Context) {
	var req struct {
		LocationID string `json:"locationId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.LocationID == "" {
		web.Fail(c, http.StatusBadRequest, `send {"locationId": "gid://shopify/Location/…"}`)
		return
	}
	_, err := h.store.SetLocation(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, req.LocationID)
	if errors.Is(err, ErrLocationNotFound) {
		web.Fail(c, http.StatusBadRequest, "that location isn't in the store")
		return
	}
	if failShopify(c, err) {
		return
	}
	h.getConnection(c)
}

// failShopify responds to the errors of a call to Shopify, reporting whether there was one.
func failShopify(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, ErrNotConfigured):
		web.Fail(c, http.StatusServiceUnavailable, "Shopify isn't set up on this server yet")
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "no store is connected")
	case errors.Is(err, ErrReconnect):
		web.Fail(c, http.StatusConflict, "Shopify no longer accepts Plant Stock's access; connect the store again")
	case errors.Is(err, ErrUnavailable):
		web.Fail(c, http.StatusServiceUnavailable, "Shopify is busy or unreachable; try again in a minute")
	default:
		web.FailInternal(c, err)
	}
	return true
}

// checkDrift handles POST /orgs/:orgId/shopify/check: compare the store with Plant Stock now.
func (h handler) checkDrift(c *gin.Context) {
	switch err := h.store.QueueDriftCheck(c.Request.Context(), organization.CurrentAccess(c).OrganizationID); {
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "no store is connected")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusAccepted)
	}
}

// syncAll handles POST /orgs/:orgId/shopify/sync: push every product again (the worker does it).
func (h handler) syncAll(c *gin.Context) {
	switch err := h.store.QueueSyncAll(c.Request.Context(), organization.CurrentAccess(c).OrganizationID); {
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "no store is connected")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusAccepted)
	}
}

type productStatusResponse struct {
	ProductID int64      `json:"productId"`
	Title     string     `json:"title"`
	AdminURL  *string    `json:"adminUrl"` // the product in the Shopify admin, once created
	SyncedAt  *time.Time `json:"syncedAt"`
	Error     *string    `json:"error"` // why Shopify refused it last time
}

// listProducts handles GET /orgs/:orgId/shopify/products: how each product stands on the store.
func (h handler) listProducts(c *gin.Context) {
	rows, err := h.store.ProductStatuses(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if failShopify(c, err) {
		return
	}
	response := make([]productStatusResponse, len(rows))
	for i, row := range rows {
		response[i] = productStatusResponse{ProductID: row.ProductID, Title: row.Title, SyncedAt: row.SyncedAt, Error: row.SyncError}
		if row.ShopifyProductID != nil {
			url := adminProductURL(row.Shop, *row.ShopifyProductID)
			response[i].AdminURL = &url
		}
	}
	c.JSON(http.StatusOK, response)
}

// adminProductURL links to a product in the Shopify admin: gid://shopify/Product/123 on
// root-access.myshopify.com is admin.shopify.com/store/root-access/products/123.
func adminProductURL(shop, productGID string) string {
	store := strings.TrimSuffix(shop, ".myshopify.com")
	return "https://admin.shopify.com/store/" + store + "/products/" + productGID[strings.LastIndex(productGID, "/")+1:]
}

// ordersStatus handles GET /orgs/:orgId/shopify/orders/status: whether orders are arriving.
func (h handler) ordersStatus(c *gin.Context) {
	connection, err := h.store.Connection(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	switch {
	case errors.Is(err, ErrNotConnected):
		c.JSON(http.StatusOK, gin.H{"connected": false})
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.JSON(http.StatusOK, gin.H{"connected": true, "checkedAt": connection.OrdersCheckedThrough, "error": connection.OrdersError})
	}
}

// importOrders handles POST /orgs/:orgId/shopify/orders/import ("Check now").
func (h handler) importOrders(c *gin.Context) {
	switch err := h.store.QueueOrderImport(c.Request.Context(), organization.CurrentAccess(c).OrganizationID); {
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "no store is connected")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusAccepted)
	}
}

// webhook handles POST /shopify/webhooks: Shopify telling Plant Stock something changed. It only
// queues work, and answers quickly (Shopify retries slow or failed deliveries).
func (h handler) webhook(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil || !h.store.Configured() || !h.store.app.VerifyWebhook(body, c.GetHeader("X-Shopify-Hmac-Sha256")) {
		c.Status(http.StatusUnauthorized)
		return
	}
	topic, shop := c.GetHeader("X-Shopify-Topic"), c.GetHeader("X-Shopify-Shop-Domain")
	if err := h.store.HandleWebhook(c.Request.Context(), topic, shop, body); err != nil {
		web.FailInternal(c, err) // Shopify retries
		return
	}
	c.Status(http.StatusOK)
}
