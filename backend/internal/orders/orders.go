// Package orders shows online orders and the plants held for them: what staff pull for each pickup.
// Orders arrive from Shopify (internal/shopify imports them and holds the plants).
package orders

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Statuses an order can have: open (to prepare), ready for pickup (in the locker), then picked up;
// or cancelled.
var statuses = []string{"open", "ready_for_pickup", "picked_up", "cancelled"}

// JobFulfillOrder fulfills a picked-up order in Shopify, then sells its plants (internal/shopify).
const JobFulfillOrder = "shopify.fulfill_order"

// JobUpdateOrderPickup copies an order's changed pickup time to its Shopify order (the Pickup date and
// Pickup window attributes), so the admin and the order status page show it (internal/shopify).
const JobUpdateOrderPickup = "shopify.update_order_pickup"

var (
	ErrNotFound   = errors.New("order not found")
	ErrWrongState = errors.New("the order isn't at that step")
)

type Store struct {
	conn db.Conn
}

func NewStore(conn db.Conn) *Store { return &Store{conn: conn} }

// Order is an online order with its items.
type Order struct {
	dbgen.ListOrdersRow
	Items []dbgen.ListOrderItemsRow
}

// List returns the nursery's orders with status, newest first (at most 200).
func (s *Store) List(ctx context.Context, orgID int64, status string) (orders []Order, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		rows, err := q.ListOrders(ctx, dbgen.ListOrdersParams{OrganizationID: orgID, Status: status})
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]int64, len(rows))
		byID := map[int64]int{}
		for i, row := range rows {
			ids[i], byID[row.ID] = row.ID, i
			orders = append(orders, Order{ListOrdersRow: row})
		}
		items, err := q.ListOrderItems(ctx, dbgen.ListOrderItemsParams{OrganizationID: orgID, OrderIds: ids})
		for _, item := range items {
			i := byID[item.OrderID]
			orders[i].Items = append(orders[i].Items, item)
		}
		return err
	})
	return orders, err
}

// MarkReady records that an order is ready for pickup (in the locker). It fails with ErrNotFound
// or ErrWrongState (not waiting to be prepared).
func (s *Store) MarkReady(ctx context.Context, orgID, orderID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.MarkOrderReady(ctx, dbgen.MarkOrderReadyParams{OrganizationID: orgID, ID: orderID})
		return s.stateError(ctx, q, orgID, orderID, err)
	})
}

// MarkNotReady moves an order marked ready for pickup back to "to prepare". It fails with
// ErrNotFound or ErrWrongState. (Picked up can't be undone: a sale is only reversed by a return.)
func (s *Store) MarkNotReady(ctx context.Context, orgID, orderID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.MarkOrderNotReady(ctx, dbgen.MarkOrderNotReadyParams{OrganizationID: orgID, ID: orderID})
		return s.stateError(ctx, q, orgID, orderID, err)
	})
}

// MarkPickedUp records that the customer has the order. The worker then fulfills it in Shopify and
// sells its plants, in that order: Shopify subtracts fulfilled stock itself, so selling first would
// count the plants twice. It fails with ErrNotFound or ErrWrongState (already picked up or cancelled).
func (s *Store) MarkPickedUp(ctx context.Context, orgID, orderID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.MarkOrderPickedUp(ctx, dbgen.MarkOrderPickedUpParams{OrganizationID: orgID, ID: orderID})
		if err := s.stateError(ctx, q, orgID, orderID, err); err != nil {
			return err
		}
		return jobs.Enqueue(ctx, q, orgID, JobFulfillOrder, map[string]int64{"orderId": orderID})
	})
}

// stateError explains a status change that matched no order: none with that ID, or not at that step.
func (s *Store) stateError(ctx context.Context, q *dbgen.Queries, orgID, orderID int64, err error) error {
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID}); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return ErrWrongState
}

// Routes adds the order routes to signedIn, a group behind auth.RequireUser.
func Routes(signedIn *gin.RouterGroup, orgs *organization.Store, store *Store) {
	h := handler{store: store}
	org := signedIn.Group("/orgs/:orgId", organization.RequireMember(orgs))
	org.GET("/orders", organization.RequirePermission(organization.InventoryView), h.list)
	org.POST("/orders/:orderId/ready", organization.RequirePermission(organization.OrdersPrepare), h.markReady)
	org.POST("/orders/:orderId/not-ready", organization.RequirePermission(organization.OrdersPrepare), h.markNotReady)
	org.POST("/orders/:orderId/picked-up", organization.RequirePermission(organization.OrdersPrepare), h.markPickedUp)
	org.POST("/orders/:orderId/pickup", organization.RequirePermission(organization.OrdersPrepare), h.movePickup)
	returnRoutes(org, h)
}

type handler struct{ store *Store }

type orderResponse struct {
	ID                       int64          `json:"id"`
	Name                     string         `json:"name"` // "#1001", as the customer sees it
	Status                   string         `json:"status"`
	PlacedAt                 time.Time      `json:"placedAt"`
	CancelledAt              *time.Time     `json:"cancelledAt"`
	ShopifyFulfillmentStatus *string        `json:"shopifyFulfillmentStatus"`
	ReadyAt                  *time.Time     `json:"readyAt"`
	ReadyByName              *string        `json:"readyByName"`
	PickedUpAt               *time.Time     `json:"pickedUpAt"`
	PickedUpByName           *string        `json:"pickedUpByName"`
	AdminURL                 string         `json:"adminUrl"`        // the order in the Shopify admin
	PickupDate               *string        `json:"pickupDate"`      // 2026-10-24, as the cart chose
	PickupWindow             *string        `json:"pickupWindow"`    // the window's name
	PickupNote               *string        `json:"pickupNote"`      // the shopper's note, if any
	PickupProblem            *string        `json:"pickupProblem"`   // why its booking needs staff (a clash, no valid slot)
	PickupChangedAt          *time.Time     `json:"pickupChangedAt"` // when the customer last changed the time (My pickup page)
	ReadyBy                  *time.Time     `json:"readyBy"`         // when it must be in the locker: its booked window's start
	LockerCode               *string        `json:"lockerCode"`      // the locker PIN for it; only to those who prepare orders
	Items                    []itemResponse `json:"items"`
}

type itemResponse struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"`
	SKU          *string  `json:"sku"`
	PotSize      *string  `json:"potSize"`
	Quantity     int32    `json:"quantity"`
	PriceCents   *int32   `json:"priceCents"`
	InPlantStock bool     `json:"inPlantStock"` // false: not one of Plant Stock's SKUs, so nothing is held
	HeldBarcodes []string `json:"heldBarcodes"` // its plants (to pull, or sold); fewer than quantity when oversold
	// The plant the shopper chose from its photo (none for items bought without choosing). Not among
	// heldBarcodes when it couldn't be held: it was no longer for sale.
	ChosenBarcode *string `json:"chosenBarcode"`
}

// markReady handles POST /orgs/:orgId/orders/:orderId/ready.
func (h handler) markReady(c *gin.Context) {
	h.changeStatus(c, h.store.MarkReady, "the order isn't waiting to be prepared")
}

// MovePickup moves an order still to collect to another pickup slot, as staff (agreed with the
// customer: the lead time and booking range don't apply, a closed, ended or taken window does). Any
// pickup problem is cleared, and the Shopify order's Pickup date and window follow (a job). It fails with
// ErrNotFound, ErrWrongState (picked up or cancelled), or pickup's ErrNotFound, ErrClosed, ErrTooSoon or
// ErrTaken.
func (s *Store) MovePickup(ctx context.Context, orgID, orderID int64, date time.Time, windowID int64, now time.Time) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		order, err := q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if order.Status != "open" && order.Status != "ready_for_pickup" {
			return ErrWrongState
		}
		before, err := q.OrderPickupBooking(ctx, dbgen.OrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		booking, err := pickup.MoveOrderBooking(ctx, q, orgID, orderID, date, windowID, now, true)
		if err != nil || booking.ID == before.ID { // the slot it already had: nothing changes
			return err
		}
		if err := q.SetOrderPickupSlot(ctx, dbgen.SetOrderPickupSlotParams{
			OrganizationID: orgID, ID: orderID, PickupDate: &booking.Date, PickupWindow: &booking.WindowName,
		}); err != nil {
			return err
		}
		return jobs.Enqueue(ctx, q, orgID, JobUpdateOrderPickup, map[string]int64{"orderId": orderID})
	})
}

type movePickupRequest struct {
	Date     string `json:"date"` // 2026-10-27
	WindowID int64  `json:"windowId"`
}

// movePickup handles POST /orgs/:orgId/orders/:orderId/pickup {date, windowId}.
func (h handler) movePickup(c *gin.Context) {
	orderID, ok := web.IDParam(c, "orderId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "order not found")
		return
	}
	var req movePickupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.WindowID <= 0 {
		web.Fail(c, http.StatusBadRequest, "date and windowId are required")
		return
	}
	date, err := pickup.ParseDate(req.Date)
	if err != nil {
		web.Fail(c, http.StatusBadRequest, "date must be a date like 2026-10-27")
		return
	}
	switch err := h.store.MovePickup(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, orderID, date, req.WindowID, time.Now()); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "order not found")
	case errors.Is(err, ErrWrongState):
		web.Fail(c, http.StatusConflict, "only orders still to collect can be moved")
	case errors.Is(err, pickup.ErrNotFound):
		web.Fail(c, http.StatusNotFound, "that pickup window doesn't exist")
	case errors.Is(err, pickup.ErrClosed):
		web.Fail(c, http.StatusConflict, "that time is closed on the Pickup calendar: reopen it there first")
	case errors.Is(err, pickup.ErrTooSoon):
		web.Fail(c, http.StatusConflict, "that time has already ended")
	case errors.Is(err, pickup.ErrTaken):
		web.Fail(c, http.StatusConflict, "another order already has that time")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

// markNotReady handles POST /orgs/:orgId/orders/:orderId/not-ready.
func (h handler) markNotReady(c *gin.Context) {
	h.changeStatus(c, h.store.MarkNotReady, "the order isn't marked ready for pickup")
}

// markPickedUp handles POST /orgs/:orgId/orders/:orderId/picked-up.
func (h handler) markPickedUp(c *gin.Context) {
	h.changeStatus(c, h.store.MarkPickedUp, "the order was already picked up or cancelled")
}

func (h handler) changeStatus(c *gin.Context, change func(ctx context.Context, orgID, orderID int64) error, wrongState string) {
	orderID, ok := web.IDParam(c, "orderId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "order not found")
		return
	}
	switch err := change(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, orderID); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "order not found")
	case errors.Is(err, ErrWrongState):
		web.Fail(c, http.StatusConflict, wrongState)
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

// list handles GET /orgs/:orgId/orders?status=open (the default), ready_for_pickup, picked_up, or
// cancelled.
func (h handler) list(c *gin.Context) {
	status := c.DefaultQuery("status", "open")
	if !validStatus(status) {
		web.Fail(c, http.StatusBadRequest, "status must be open, ready_for_pickup, picked_up, or cancelled")
		return
	}
	orders, err := h.store.List(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, status)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	// The locker code opens the locker: only for those who load it.
	seesCode := organization.Has(organization.CurrentAccess(c).Role, organization.OrdersPrepare)
	response := make([]orderResponse, len(orders))
	for i, o := range orders {
		response[i] = orderResponse{
			ID: o.ID, Name: o.Name, Status: o.Status, PlacedAt: o.PlacedAt, CancelledAt: o.CancelledAt,
			ShopifyFulfillmentStatus: o.ShopifyFulfillmentStatus, AdminURL: adminOrderURL(o.Shop, o.ShopifyOrderID),
			ReadyAt: o.ReadyAt, ReadyByName: o.ReadyByName, PickedUpAt: o.PickedUpAt, PickedUpByName: o.PickedUpByName,
			PickupWindow: o.PickupWindow, PickupNote: o.PickupNote, PickupProblem: o.PickupProblem, PickupChangedAt: o.PickupChangedAt,
			ReadyBy: o.ReadyBy,
			Items:   make([]itemResponse, len(o.Items)),
		}
		if o.PickupDate != nil {
			date := o.PickupDate.Format(time.DateOnly)
			response[i].PickupDate = &date
		}
		if seesCode {
			response[i].LockerCode = &o.LockerCode
		}
		for j, item := range o.Items {
			response[i].Items[j] = itemResponse{
				ID: item.ID, Title: item.Title, SKU: item.Sku, PotSize: item.PotSize, Quantity: item.Quantity,
				PriceCents: item.PriceCents, InPlantStock: item.VariantID != nil, HeldBarcodes: item.HeldBarcodes,
				ChosenBarcode: item.ChosenBarcode,
			}
		}
	}
	c.JSON(http.StatusOK, response)
}

func validStatus(status string) bool { return slices.Contains(statuses, status) }

// adminOrderURL links to an order in the Shopify admin: gid://shopify/Order/123 on
// root-access.myshopify.com is admin.shopify.com/store/root-access/orders/123.
func adminOrderURL(shop, orderGID string) string {
	store := strings.TrimSuffix(shop, ".myshopify.com")
	return "https://admin.shopify.com/store/" + store + "/orders/" + orderGID[strings.LastIndex(orderGID, "/")+1:]
}
