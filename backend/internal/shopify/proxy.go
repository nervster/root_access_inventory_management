package shopify

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// The storefront (the nursery's Shopify theme) calls Plant Stock through the app's App Proxy: a path on
// the store's own domain, /apps/plant-stock/…, that Shopify forwards to /api/shopify/proxy/… with a
// signature (shopify/shopify.app.<site>.toml, [app_proxy]). No session, no cookies: the signature is the
// only proof the request came through the store, and it says which store, not who the shopper is.

const proxyOrganizationKey = "shopify.proxyOrganization"

func proxyRoutes(api *gin.RouterGroup, h handler) {
	proxy := api.Group("/shopify/proxy", h.requireProxy)
	proxy.GET("/pickup-slots", h.pickupSlots)
	proxy.POST("/pickup-holds", h.placeHold)
	proxy.POST("/pickup-holds/refresh", h.refreshHold)
	proxy.POST("/pickup-holds/release", h.releaseHold)
	myPickupRoutes(proxy, h)
}

// requireProxy lets through only requests Shopify signed, from a store a nursery has connected, and
// notes which nursery.
func (h handler) requireProxy(c *gin.Context) {
	query := c.Request.URL.Query()
	if !h.store.Configured() || !h.store.app.VerifyProxy(query) || !ValidShop(query.Get("shop")) {
		web.Fail(c, http.StatusUnauthorized, "not a signed request from the store")
		c.Abort()
		return
	}
	switch orgID, err := h.store.OrganizationForShop(c.Request.Context(), query.Get("shop")); {
	case errors.Is(err, ErrNotConnected):
		web.Fail(c, http.StatusNotFound, "this store isn't connected to Plant Stock")
		c.Abort()
	case err != nil:
		web.FailInternal(c, err)
		c.Abort()
	default:
		c.Set(proxyOrganizationKey, orgID)
		c.Next()
	}
}

// OrganizationForShop returns the nursery connected to shop, or fails with ErrNotConnected.
func (s *Store) OrganizationForShop(ctx context.Context, shop string) (int64, error) {
	orgID, err := dbgen.New(s.conn).ShopifyConnectionByShop(ctx, shop) // account-level: the nursery isn't known yet
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotConnected
	}
	return orgID, err
}

type proxySlotResponse struct {
	WindowID int64     `json:"windowId"`
	Window   string    `json:"window"` // its name, e.g. "8 AM – 2 PM" or "Morning": save it as the Pickup window
	Start    time.Time `json:"start"`  // with the nursery's offset, e.g. 2026-10-24T08:00:00-05:00
	End      time.Time `json:"end"`
}

type proxyDayResponse struct {
	Date  string              `json:"date"`  // 2026-10-24: save it as the Pickup date
	Slots []proxySlotResponse `json:"slots"` // empty: nothing left to book that day
}

type pickupSlotsResponse struct {
	TimeZone    string             `json:"timeZone"`
	LeadHours   int                `json:"leadHours"`
	BookingDays int                `json:"bookingDays"`
	Days        []proxyDayResponse `json:"days"`
}

// pickupSlots handles GET /apps/plant-stock/pickup-slots (proxied): every slot customers can book now,
// for each date from today through the booking range, so the cart loads them all in one call and can
// mark full days. Closed, too-soon and too-far slots are left out.
func (h handler) pickupSlots(c *gin.Context) {
	rules, days, err := h.slots.OpenSlots(c.Request.Context(), c.GetInt64(proxyOrganizationKey), h.now(), c.Query("cartToken"))
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := pickupSlotsResponse{TimeZone: rules.Location.String(), LeadHours: rules.LeadHours, BookingDays: rules.BookingDays, Days: []proxyDayResponse{}}
	for _, day := range days {
		d := proxyDayResponse{Date: day.Date.Format(time.DateOnly), Slots: []proxySlotResponse{}}
		for _, slot := range day.Slots {
			d.Slots = append(d.Slots, proxySlotResponse{WindowID: slot.ID, Window: slot.Name, Start: slot.Start.In(rules.Location), End: slot.End.In(rules.Location)})
		}
		response.Days = append(response.Days, d)
	}
	c.Header("Cache-Control", "no-store") // slots change as they're closed and booked
	c.JSON(http.StatusOK, response)
}

// proxyFail answers the storefront with a code it acts on and a sentence it can show
// (docs/pickup-storefront.md, Errors).
func proxyFail(c *gin.Context, status int, code, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(status, gin.H{"error": code, "message": message})
}

// failHold explains why a hold couldn't be placed or kept.
func failHold(c *gin.Context, err error) {
	switch {
	case errors.Is(err, pickup.ErrTaken):
		proxyFail(c, http.StatusConflict, "taken", "That pickup time was just taken. Please choose another.")
	case errors.Is(err, pickup.ErrTooSoon):
		proxyFail(c, http.StatusConflict, "too_soon", "That pickup time is too soon now. Please choose a later one.")
	case errors.Is(err, pickup.ErrClosed):
		proxyFail(c, http.StatusConflict, "closed", "That pickup time isn't available any more. Please choose another.")
	case errors.Is(err, pickup.ErrTooFar):
		proxyFail(c, http.StatusConflict, "too_far", "That date is too far ahead. Please choose an earlier one.")
	case errors.Is(err, pickup.ErrNotFound):
		proxyFail(c, http.StatusNotFound, "not_found", "That pickup time isn't available any more. Please choose another.")
	case errors.Is(err, pickup.ErrTooManyHolds):
		proxyFail(c, http.StatusTooManyRequests, "too_many_holds", "Too many pickup times are being held. Please finish an order first, or try again in 30 minutes.")
	default:
		web.FailInternal(c, err)
	}
}

type holdResponse struct {
	HoldID    string    `json:"holdId"` // save it as the Pickup hold
	Date      string    `json:"date"`
	WindowID  int64     `json:"windowId"`
	Window    string    `json:"window"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// respondHold answers with the hold, times in the nursery's time zone.
func (h handler) respondHold(c *gin.Context, status int, hold pickup.Hold) {
	loc := time.UTC
	if rules, err := h.slots.Rules(c.Request.Context(), c.GetInt64(proxyOrganizationKey)); err == nil {
		loc = rules.Location
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(status, holdResponse{
		HoldID: hold.ID, Date: hold.Date.Format(time.DateOnly), WindowID: hold.WindowID, Window: hold.Window,
		Start: hold.Start.In(loc), End: hold.End.In(loc), ExpiresAt: hold.ExpiresAt.In(loc),
	})
}

type holdRequest struct {
	CartToken string `json:"cartToken"`
	Date      string `json:"date"`     // placing a hold
	WindowID  int64  `json:"windowId"` // placing a hold
	HoldID    string `json:"holdId"`   // refreshing or releasing one
}

// bindHold reads a hold request; it answers invalid (and returns false) when cartToken, or what needs
// holds (holdId, or date and windowId), is missing.
func bindHold(c *gin.Context, needsHoldID bool) (holdRequest, bool) {
	var req holdRequest
	err := c.ShouldBindJSON(&req)
	req.CartToken = strings.TrimSpace(req.CartToken)
	switch {
	case err != nil:
		proxyFail(c, http.StatusBadRequest, "invalid", "The request must be JSON.")
	case req.CartToken == "" || len(req.CartToken) > 200:
		proxyFail(c, http.StatusBadRequest, "invalid", "cartToken is required (the token from /cart.js).")
	case needsHoldID && req.HoldID == "":
		proxyFail(c, http.StatusBadRequest, "invalid", "holdId is required.")
	default:
		return req, true
	}
	return req, false
}

// clientIP is the shopper's address, as Shopify forwards it: the first X-Forwarded-For entry (the
// proxies after Shopify add theirs after it).
func clientIP(c *gin.Context) string {
	if first, _, _ := strings.Cut(c.GetHeader("X-Forwarded-For"), ","); strings.TrimSpace(first) != "" {
		return strings.TrimSpace(first)
	}
	return c.ClientIP()
}

// placeHold handles POST /apps/plant-stock/pickup-holds {cartToken, date, windowId}: holds the slot for
// the cart for 30 minutes, giving back the cart's other hold.
func (h handler) placeHold(c *gin.Context) {
	req, ok := bindHold(c, false)
	if !ok {
		return
	}
	date, err := pickup.ParseDate(req.Date)
	if err != nil || req.WindowID <= 0 {
		proxyFail(c, http.StatusBadRequest, "invalid", "date (like 2026-10-24) and windowId are required.")
		return
	}
	hold, err := h.slots.PlaceHold(c.Request.Context(), c.GetInt64(proxyOrganizationKey), req.CartToken, clientIP(c), date, req.WindowID, h.now())
	if err != nil {
		failHold(c, err)
		return
	}
	h.respondHold(c, http.StatusCreated, hold)
}

// refreshHold handles POST /apps/plant-stock/pickup-holds/refresh {cartToken, holdId}: called at
// Checkout; keeps the hold another 30 minutes if the slot is still fine.
func (h handler) refreshHold(c *gin.Context) {
	req, ok := bindHold(c, true)
	if !ok {
		return
	}
	hold, err := h.slots.RefreshHold(c.Request.Context(), c.GetInt64(proxyOrganizationKey), req.CartToken, req.HoldID, h.now())
	if err != nil {
		failHold(c, err)
		return
	}
	h.respondHold(c, http.StatusOK, hold)
}

// releaseHold handles POST /apps/plant-stock/pickup-holds/release {cartToken, holdId}: gives the slot
// back (an emptied cart, or one that's over 4 plants).
func (h handler) releaseHold(c *gin.Context) {
	req, ok := bindHold(c, true)
	if !ok {
		return
	}
	if err := h.slots.ReleaseHold(c.Request.Context(), c.GetInt64(proxyOrganizationKey), req.CartToken, req.HoldID); err != nil {
		web.FailInternal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
