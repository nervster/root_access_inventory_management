package shopify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/orders"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// The store's My pickup page (docs/pickup-storefront.md, "After the order"): a customer finds their order
// by its number and email, then changes its pickup time or cancels it. Through the App Proxy, like the cart.

const (
	myPickupKeyLife  = time.Hour        // how long a lookup's key works
	lookupsAllowed   = 10               // per visitor, per lookupPeriod
	lookupPeriod     = 15 * time.Minute //
	cancelAfterReady = 2 * time.Hour    // an order can be cancelled until it's been ready this long
)

func myPickupRoutes(proxy *gin.RouterGroup, h handler) {
	proxy.POST("/my-pickup/lookup", h.lookupOrder)
	proxy.POST("/my-pickup", h.currentOrder)
	proxy.POST("/my-pickup/change", h.changePickup)
	proxy.POST("/my-pickup/cancel", h.cancelOrder)
}

// lookupLimiter counts each visitor's lookups, so the form can't be used to guess order numbers. Kept in
// memory: the API runs as one process, and a restart only forgives a few lookups.
type lookupLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func newLookupLimiter() *lookupLimiter { return &lookupLimiter{seen: map[string][]time.Time{}} }

// allow records a lookup by visitor at now and reports whether it's within the limit.
func (l *lookupLimiter) allow(visitor string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.seen[visitor][:0]
	for _, at := range l.seen[visitor] {
		if now.Sub(at) < lookupPeriod {
			recent = append(recent, at)
		}
	}
	if len(recent) >= lookupsAllowed {
		l.seen[visitor] = recent
		return false
	}
	l.seen[visitor] = append(recent, now)
	if len(l.seen) > 10_000 { // forget old visitors now and then
		for v, times := range l.seen {
			if len(times) == 0 || now.Sub(times[len(times)-1]) >= lookupPeriod {
				delete(l.seen, v)
			}
		}
	}
	return true
}

// A key proves, for an hour, that this visitor gave the order's number and email: the nursery, the
// order and the expiry, signed with the app's secret (so nobody can make one up).
type myPickupKey struct {
	OrganizationID int64 `json:"o"`
	OrderID        int64 `json:"i"`
	Expires        int64 `json:"e"` // unix seconds
}

func (h handler) signKey(k myPickupKey) string {
	payload, _ := json.Marshal(k)
	body := base64.RawURLEncoding.EncodeToString(payload)
	return "mp_" + body + "." + base64.RawURLEncoding.EncodeToString(h.keyMAC(body))
}

func (h handler) keyMAC(body string) []byte {
	mac := hmac.New(sha256.New, []byte(h.store.app.ClientSecret))
	mac.Write([]byte("my-pickup\n" + body))
	return mac.Sum(nil)
}

// readKey returns the order a key names, if it's genuine, unexpired, and for this nursery.
func (h handler) readKey(text string, orgID int64, now time.Time) (int64, bool) {
	body, signature, ok := strings.Cut(strings.TrimPrefix(text, "mp_"), ".")
	given, err := base64.RawURLEncoding.DecodeString(signature)
	if !ok || !strings.HasPrefix(text, "mp_") || err != nil || !hmac.Equal(given, h.keyMAC(body)) {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	var k myPickupKey
	if err != nil || json.Unmarshal(payload, &k) != nil || k.OrganizationID != orgID || now.Unix() >= k.Expires {
		return 0, false
	}
	return k.OrderID, true
}

type myPickupSlot struct {
	Date     string    `json:"date"`
	WindowID int64     `json:"windowId"`
	Window   string    `json:"window"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// myPickupOrder is the order as the page shows it: what was ordered and paid, nothing about the customer.
type myPickupOrder struct {
	Order         string           `json:"order"` // "#1042"
	Status        string           `json:"status"`
	CanChangeTime bool             `json:"canChangeTime"`
	CanCancel     bool             `json:"canCancel"`
	CancelUntil   *time.Time       `json:"cancelUntil"`
	Pickup        *myPickupSlot    `json:"pickup"`
	Items         []myPickupItem   `json:"items,omitempty"`   // left out if Shopify couldn't be asked
	Amounts       *myPickupAmounts `json:"amounts,omitempty"` // ditto
}

type myPickupItem struct {
	Title      string  `json:"title"`
	Variant    *string `json:"variant"` // null for a product's default variant
	Quantity   int     `json:"quantity"`
	TotalCents int64   `json:"totalCents"` // the line after all its discounts
}

// myPickupAmounts are the order's money, in the shop's currency, as cents. The subtotal is before
// discounts (Shopify's is after them), so subtotal − discount + tax = total on a pickup order.
type myPickupAmounts struct {
	Currency      string `json:"currency"`
	SubtotalCents int64  `json:"subtotalCents"`
	DiscountCents int64  `json:"discountCents"`
	TaxCents      int64  `json:"taxCents"`
	TotalCents    int64  `json:"totalCents"` // what was charged
	RefundedCents int64  `json:"refundedCents"`
}

// orderDetails is what Plant Stock asks Shopify about an order for the page, each time (none of it is
// stored): the email, to check a lookup, and what was ordered and paid.
type orderDetails struct {
	Email   *string
	Items   []myPickupItem
	Amounts *myPickupAmounts
}

// pickupRules are when a customer may still change or cancel: change until pickup; cancel while it's
// prepared, and for cancelAfterReady after staff mark it ready.
func pickupRules(order dbgen.Order, now time.Time) (canChange, canCancel bool, cancelUntil *time.Time) {
	switch order.Status {
	case "open":
		return true, true, nil
	case "ready_for_pickup":
		if order.ReadyAt == nil {
			return true, false, nil
		}
		until := order.ReadyAt.Add(cancelAfterReady)
		if now.Before(until) {
			return true, true, &until
		}
		return true, false, nil
	}
	return false, false, nil
}

// orderView loads the order and its booking as the page sees them, times in the nursery's time zone,
// with what was ordered and paid from Shopify (details, if the caller asked already; if Shopify can't be
// reached, those are left out and the page hides them).
func (h handler) orderView(ctx context.Context, orgID, orderID int64, now time.Time, details *orderDetails) (dbgen.Order, myPickupOrder, error) {
	rules, err := h.slots.Rules(ctx, orgID)
	if err != nil {
		return dbgen.Order{}, myPickupOrder{}, err
	}
	var order dbgen.Order
	var booking *dbgen.PickupBooking
	err = db.InTenant(ctx, h.store.conn, orgID, func(q *dbgen.Queries) error {
		if order, err = q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID}); err != nil {
			return err
		}
		b, err := q.OrderPickupBooking(ctx, dbgen.OrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID})
		if err == nil {
			booking = &b
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return nil
	})
	if err != nil {
		return dbgen.Order{}, myPickupOrder{}, err
	}
	view := myPickupOrder{Order: order.Name, Status: order.Status}
	view.CanChangeTime, view.CanCancel, view.CancelUntil = pickupRules(order, now)
	if view.CancelUntil != nil {
		local := view.CancelUntil.In(rules.Location)
		view.CancelUntil = &local
	}
	if booking != nil {
		slot := myPickupSlot{Date: booking.Date.Format(time.DateOnly), Window: booking.WindowName,
			Start: booking.StartsAt.In(rules.Location), End: booking.EndsAt.In(rules.Location)}
		if booking.WindowID != nil {
			slot.WindowID = *booking.WindowID
		}
		view.Pickup = &slot
	}
	if details == nil {
		if details, err = h.store.OrderDetails(ctx, orgID, order.ShopifyOrderID); err != nil {
			slog.Warn("my pickup page: no order details from Shopify", "organization", orgID, "order", order.Name, "error", err)
		}
	}
	if details != nil {
		view.Items, view.Amounts = details.Items, details.Amounts
	}
	return order, view, nil
}

// unavailable answers when Plant Stock or Shopify can't be reached for the order.
func unavailable(c *gin.Context, err error) {
	slog.Error("my pickup page", "error", err)
	proxyFail(c, http.StatusServiceUnavailable, "unavailable", "We can't reach your order right now. Please try again shortly.")
}

func tooLate(c *gin.Context, view myPickupOrder, message string) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusConflict, gin.H{"error": "too_late", "message": message, "order": view})
}

// orderFromKey reads the request's key; it answers expired (and returns false) when it isn't valid.
func (h handler) orderFromKey(c *gin.Context, key string) (int64, bool) {
	orderID, ok := h.readKey(key, c.GetInt64(proxyOrganizationKey), h.now())
	if !ok {
		proxyFail(c, http.StatusUnauthorized, "expired", "Please enter your order number and email again.")
	}
	return orderID, ok
}

type lookupRequest struct {
	OrderNumber string `json:"orderNumber"` // "1042" or "#1042"
	Email       string `json:"email"`
}

// lookupOrder handles POST /apps/plant-stock/my-pickup/lookup: finds the order by its number and checks
// the email against Shopify's (asked now, never stored). A wrong number and a wrong email get the same
// answer, so the form can't reveal which email belongs to which order.
func (h handler) lookupOrder(c *gin.Context) {
	var req lookupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		proxyFail(c, http.StatusBadRequest, "invalid", "The request must be JSON.")
		return
	}
	number := strings.TrimPrefix(strings.ReplaceAll(req.OrderNumber, " ", ""), "#")
	email := strings.TrimSpace(req.Email)
	if number == "" || strings.Trim(number, "0123456789") != "" || email == "" {
		proxyFail(c, http.StatusBadRequest, "invalid", "orderNumber (digits) and email are required.")
		return
	}
	ctx, orgID, now := c.Request.Context(), c.GetInt64(proxyOrganizationKey), h.now()
	if !h.lookups.allow(fmt.Sprintf("%d/%s", orgID, clientIP(c)), now) {
		proxyFail(c, http.StatusTooManyRequests, "too_many_lookups", "Too many tries. Please wait 15 minutes and try again.")
		return
	}
	notFound := func() {
		proxyFail(c, http.StatusNotFound, "not_found", "We couldn't find an order with that number and email.")
	}

	var order dbgen.Order
	err := db.InTenant(ctx, h.store.conn, orgID, func(q *dbgen.Queries) (err error) {
		order, err = q.GetOrderByName(ctx, dbgen.GetOrderByNameParams{OrganizationID: orgID, Name: "#" + number})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		notFound()
		return
	} else if err != nil {
		unavailable(c, err)
		return
	}
	details, err := h.store.OrderDetails(ctx, orgID, order.ShopifyOrderID)
	if err != nil {
		unavailable(c, err)
		return
	}
	if details == nil || details.Email == nil || !strings.EqualFold(strings.TrimSpace(*details.Email), email) {
		notFound()
		return
	}

	_, view, err := h.orderView(ctx, orgID, order.ID, now, details)
	if err != nil {
		unavailable(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"key":   h.signKey(myPickupKey{OrganizationID: orgID, OrderID: order.ID, Expires: now.Add(myPickupKeyLife).Unix()}),
		"order": view,
	})
}

type keyRequest struct {
	Key      string `json:"key"`
	Date     string `json:"date"`     // change
	WindowID int64  `json:"windowId"` // change
}

func bindKey(c *gin.Context) (keyRequest, bool) {
	var req keyRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Key == "" {
		proxyFail(c, http.StatusBadRequest, "invalid", "key is required.")
		return req, false
	}
	return req, true
}

func respondOrder(c *gin.Context, view myPickupOrder) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"order": view})
}

// currentOrder handles POST /apps/plant-stock/my-pickup: the order as it is now.
func (h handler) currentOrder(c *gin.Context) {
	req, ok := bindKey(c)
	if !ok {
		return
	}
	orderID, ok := h.orderFromKey(c, req.Key)
	if !ok {
		return
	}
	_, view, err := h.orderView(c.Request.Context(), c.GetInt64(proxyOrganizationKey), orderID, h.now(), nil)
	if err != nil {
		unavailable(c, err)
		return
	}
	respondOrder(c, view)
}

// changePickup handles POST /apps/plant-stock/my-pickup/change: moves the pickup to another slot (the
// cart's rules), until the order is picked up. An order already ready stays ready, marked as changed for
// staff. The Shopify order's Pickup date and window follow (a job).
func (h handler) changePickup(c *gin.Context) {
	req, ok := bindKey(c)
	if !ok {
		return
	}
	date, err := pickup.ParseDate(req.Date)
	if err != nil || req.WindowID <= 0 {
		proxyFail(c, http.StatusBadRequest, "invalid", "date (like 2026-10-27) and windowId are required.")
		return
	}
	orderID, ok := h.orderFromKey(c, req.Key)
	if !ok {
		return
	}
	ctx, orgID, now := c.Request.Context(), c.GetInt64(proxyOrganizationKey), h.now()

	var view myPickupOrder
	err = db.InTenant(ctx, h.store.conn, orgID, func(q *dbgen.Queries) error {
		order, err := q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID})
		if err != nil {
			return err
		}
		if canChange, _, _ := pickupRules(order, now); !canChange {
			return errTooLate
		}
		before, err := q.OrderPickupBooking(ctx, dbgen.OrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		booking, err := pickup.MoveOrderBooking(ctx, q, orgID, orderID, date, req.WindowID, now, false)
		if err != nil || booking.ID == before.ID { // the slot it already had: nothing changes
			return err
		}
		if err := q.SetOrderPickupChanged(ctx, dbgen.SetOrderPickupChangedParams{
			OrganizationID: orgID, ID: orderID, PickupDate: &booking.Date, PickupWindow: &booking.WindowName, ChangedAt: &now,
		}); err != nil {
			return err
		}
		slog.Info("customer changed their pickup time", "organization", orgID, "order", order.Name, "date", req.Date, "window", booking.WindowName)
		return jobs.Enqueue(ctx, q, orgID, orders.JobUpdateOrderPickup, map[string]int64{"orderId": orderID})
	})
	switch {
	case errors.Is(err, errTooLate):
		if _, view, err = h.orderView(ctx, orgID, orderID, now, nil); err != nil {
			unavailable(c, err)
			return
		}
		tooLate(c, view, "This order can't be changed any more. Please contact the nursery.")
		return
	case errors.Is(err, pgx.ErrNoRows):
		proxyFail(c, http.StatusUnauthorized, "expired", "Please enter your order number and email again.")
		return
	case errors.Is(err, pickup.ErrTaken), errors.Is(err, pickup.ErrTooSoon), errors.Is(err, pickup.ErrClosed),
		errors.Is(err, pickup.ErrTooFar), errors.Is(err, pickup.ErrNotFound):
		failHold(c, err)
		return
	case err != nil:
		unavailable(c, err)
		return
	}
	if _, view, err = h.orderView(ctx, orgID, orderID, now, nil); err != nil {
		unavailable(c, err)
		return
	}
	respondOrder(c, view)
}

var errTooLate = errors.New("my pickup: too late")

// cancelOrder handles POST /apps/plant-stock/my-pickup/cancel: cancels the order in Shopify with a full
// refund (Shopify emails the customer), until it's been ready for 2 hours; then records it here, so its
// plants go back on sale and its slot is freed straight away.
func (h handler) cancelOrder(c *gin.Context) {
	req, ok := bindKey(c)
	if !ok {
		return
	}
	orderID, ok := h.orderFromKey(c, req.Key)
	if !ok {
		return
	}
	ctx, orgID, now := c.Request.Context(), c.GetInt64(proxyOrganizationKey), h.now()
	order, view, err := h.orderView(ctx, orgID, orderID, now, nil)
	if err != nil {
		unavailable(c, err)
		return
	}
	if !view.CanCancel {
		tooLate(c, view, "This order can't be cancelled any more. Please contact the nursery.")
		return
	}
	if err := h.store.CancelOrder(ctx, orgID, order.ShopifyOrderID); err != nil {
		unavailable(c, err)
		return
	}
	err = db.InTenant(ctx, h.store.conn, orgID, func(q *dbgen.Queries) error {
		return cancelInPlantStock(ctx, q, orgID, orderID, order.Name)
	})
	if err != nil {
		unavailable(c, err)
		return
	}
	slog.Info("customer cancelled their order", "organization", orgID, "order", order.Name)
	if _, view, err = h.orderView(ctx, orgID, orderID, now, nil); err != nil {
		unavailable(c, err)
		return
	}
	respondOrder(c, view)
}

const orderDetailsQuery = `query OrderDetails($id: ID!) {
  order(id: $id) {
    email
    lineItems(first: 100) {
      nodes { title variantTitle quantity discountedTotalSet(withCodeDiscounts: true) { shopMoney { amount } } }
    }
    subtotalPriceSet { shopMoney { amount currencyCode } }
    totalDiscountsSet { shopMoney { amount } }
    totalTaxSet { shopMoney { amount } }
    totalPriceSet { shopMoney { amount } }
    totalRefundedSet { shopMoney { amount } }
  }
}`

type shopMoney struct {
	ShopMoney struct {
		Amount       string `json:"amount"`
		CurrencyCode string `json:"currencyCode"`
	} `json:"shopMoney"`
}

func (m *shopMoney) cents() int64 {
	if m == nil {
		return 0
	}
	if c := priceCents(m.ShopMoney.Amount); c != nil {
		return int64(*c)
	}
	return 0
}

// OrderDetails asks Shopify, now, for an order's email (to check a lookup: Shopify returns it only if the
// app may read customers' email, the Email field of protected customer data) and what was ordered and
// paid. Nothing of it is stored. nil if the order is gone from Shopify.
func (s *Store) OrderDetails(ctx context.Context, orgID int64, shopifyOrderID string) (*orderDetails, error) {
	shop, token, err := s.accessToken(ctx, orgID)
	if err != nil {
		return nil, err
	}
	var data struct {
		Order *struct {
			Email     *string `json:"email"`
			LineItems struct {
				Nodes []struct {
					Title              string    `json:"title"`
					VariantTitle       *string   `json:"variantTitle"`
					Quantity           int       `json:"quantity"`
					DiscountedTotalSet shopMoney `json:"discountedTotalSet"`
				} `json:"nodes"`
			} `json:"lineItems"`
			SubtotalPriceSet  *shopMoney `json:"subtotalPriceSet"`
			TotalDiscountsSet *shopMoney `json:"totalDiscountsSet"`
			TotalTaxSet       *shopMoney `json:"totalTaxSet"`
			TotalPriceSet     *shopMoney `json:"totalPriceSet"`
			TotalRefundedSet  *shopMoney `json:"totalRefundedSet"`
		} `json:"order"`
	}
	if err := s.app.GraphQL(ctx, shop, token, orderDetailsQuery, map[string]any{"id": shopifyOrderID}, &data); err != nil {
		return nil, fmt.Errorf("read the order (if Shopify withheld its email, allow the Email field under protected customer data for the app): %w", err)
	}
	o := data.Order
	if o == nil {
		return nil, nil
	}
	details := &orderDetails{Email: o.Email, Items: []myPickupItem{}}
	for _, line := range o.LineItems.Nodes {
		variant := line.VariantTitle
		if variant != nil && (*variant == "" || *variant == "Default Title") { // Shopify's name for "no variants"
			variant = nil
		}
		details.Items = append(details.Items, myPickupItem{Title: line.Title, Variant: variant, Quantity: line.Quantity, TotalCents: line.DiscountedTotalSet.cents()})
	}
	discount := o.TotalDiscountsSet.cents()
	amounts := &myPickupAmounts{
		SubtotalCents: o.SubtotalPriceSet.cents() + discount, // Shopify's subtotal is after discounts
		DiscountCents: discount, TaxCents: o.TotalTaxSet.cents(), TotalCents: o.TotalPriceSet.cents(), RefundedCents: o.TotalRefundedSet.cents(),
	}
	if o.SubtotalPriceSet != nil {
		amounts.Currency = o.SubtotalPriceSet.ShopMoney.CurrencyCode
	}
	details.Amounts = amounts
	return details, nil
}

const orderCancelMutation = `mutation CancelOrder($id: ID!) {
  orderCancel(orderId: $id, reason: CUSTOMER, refundMethod: {originalPaymentMethodsRefund: true}, restock: false,
              notifyCustomer: true, staffNote: "Cancelled by the customer on the My pickup page") {
    orderCancelUserErrors { code field message }
  }
}`

// CancelOrder cancels an order in Shopify with a full refund to the original payment, and Shopify's
// cancellation email. Not restocked: Plant Stock sets Shopify's stock from its plants.
func (s *Store) CancelOrder(ctx context.Context, orgID int64, shopifyOrderID string) error {
	shop, token, err := s.accessToken(ctx, orgID)
	if err != nil {
		return err
	}
	var data struct {
		OrderCancel struct {
			UserErrors []struct {
				Code    *string `json:"code"`
				Message string  `json:"message"`
			} `json:"orderCancelUserErrors"`
		} `json:"orderCancel"`
	}
	if err := s.app.GraphQL(ctx, shop, token, orderCancelMutation, map[string]any{"id": shopifyOrderID}, &data); err != nil {
		return fmt.Errorf("cancel order: %w", err)
	}
	if errs := data.OrderCancel.UserErrors; len(errs) > 0 {
		return fmt.Errorf("cancel order: %s", errs[0].Message)
	}
	return nil
}

const orderAttributesQuery = `query OrderAttributes($id: ID!) { order(id: $id) { customAttributes { key value } } }`

const orderUpdateMutation = `mutation UpdateOrder($input: OrderInput!) {
  orderUpdate(input: $input) { userErrors { field message } }
}`

func (s *Store) updateOrderPickupJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		OrderID int64 `json:"orderId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return s.UpdateOrderPickup(ctx, job.OrganizationID, payload.OrderID)
}

// UpdateOrderPickup copies the order's pickup date and window to its Shopify order's Pickup date and
// Pickup window attributes. orderUpdate replaces all the attributes, so the others are read first and
// kept. Safe to run twice.
func (s *Store) UpdateOrderPickup(ctx context.Context, orgID, orderID int64) error {
	var order dbgen.Order
	err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) (err error) {
		order, err = q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (order.PickupDate == nil || order.PickupWindow == nil) {
		return nil
	} else if err != nil {
		return err
	}
	shop, token, err := s.accessToken(ctx, orgID)
	if errors.Is(err, ErrNotConnected) || errors.Is(err, ErrReconnect) {
		return nil // nothing to update until the store is connected again
	} else if err != nil {
		return err
	}
	var current struct {
		Order *struct {
			CustomAttributes []struct {
				Key   string  `json:"key"`
				Value *string `json:"value"`
			} `json:"customAttributes"`
		} `json:"order"`
	}
	if err := s.app.GraphQL(ctx, shop, token, orderAttributesQuery, map[string]any{"id": order.ShopifyOrderID}, &current); err != nil {
		return fmt.Errorf("read order attributes: %w", err)
	}
	if current.Order == nil {
		return nil // deleted in Shopify
	}
	attributes := []map[string]string{}
	for _, a := range current.Order.CustomAttributes {
		if a.Key != "Pickup date" && a.Key != "Pickup window" && a.Value != nil {
			attributes = append(attributes, map[string]string{"key": a.Key, "value": *a.Value})
		}
	}
	attributes = append(attributes,
		map[string]string{"key": "Pickup date", "value": order.PickupDate.Format(time.DateOnly)},
		map[string]string{"key": "Pickup window", "value": *order.PickupWindow})
	var data struct {
		OrderUpdate struct {
			UserErrors []struct {
				Message string `json:"message"`
			} `json:"userErrors"`
		} `json:"orderUpdate"`
	}
	input := map[string]any{"id": order.ShopifyOrderID, "customAttributes": attributes}
	if err := s.app.GraphQL(ctx, shop, token, orderUpdateMutation, map[string]any{"input": input}, &data); err != nil {
		return fmt.Errorf("update order pickup: %w", err)
	}
	if errs := data.OrderUpdate.UserErrors; len(errs) > 0 {
		return fmt.Errorf("update order pickup: %s", errs[0].Message)
	}
	return nil
}
