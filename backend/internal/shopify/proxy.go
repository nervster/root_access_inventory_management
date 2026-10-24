package shopify

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
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
	rules, days, err := h.slots.OpenSlots(c.Request.Context(), c.GetInt64(proxyOrganizationKey), h.now())
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
