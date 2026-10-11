// Package orders shows online orders and the plants held for them: what staff pull for each pickup.
// Orders arrive from Shopify (internal/shopify imports them and holds the plants).
package orders

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Statuses an order can have: open until picked up (or cancelled).
var statuses = []string{"open", "picked_up", "cancelled"}

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

// Routes adds the order routes to signedIn, a group behind auth.RequireUser.
func Routes(signedIn *gin.RouterGroup, orgs *organization.Store, store *Store) {
	h := handler{store: store}
	org := signedIn.Group("/orgs/:orgId", organization.RequireMember(orgs))
	org.GET("/orders", organization.RequirePermission(organization.InventoryView), h.list)
}

type handler struct{ store *Store }

type orderResponse struct {
	ID                       int64          `json:"id"`
	Name                     string         `json:"name"` // "#1001", as the customer sees it
	Status                   string         `json:"status"`
	PlacedAt                 time.Time      `json:"placedAt"`
	CancelledAt              *time.Time     `json:"cancelledAt"`
	ShopifyFulfillmentStatus *string        `json:"shopifyFulfillmentStatus"`
	AdminURL                 string         `json:"adminUrl"` // the order in the Shopify admin
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
	HeldBarcodes []string `json:"heldBarcodes"` // the plants to pull; fewer than quantity when oversold
}

// list handles GET /orgs/:orgId/orders?status=open (the default), picked_up, or cancelled.
func (h handler) list(c *gin.Context) {
	status := c.DefaultQuery("status", "open")
	if !validStatus(status) {
		web.Fail(c, http.StatusBadRequest, "status must be open, picked_up, or cancelled")
		return
	}
	orders, err := h.store.List(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, status)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]orderResponse, len(orders))
	for i, o := range orders {
		response[i] = orderResponse{
			ID: o.ID, Name: o.Name, Status: o.Status, PlacedAt: o.PlacedAt, CancelledAt: o.CancelledAt,
			ShopifyFulfillmentStatus: o.ShopifyFulfillmentStatus, AdminURL: adminOrderURL(o.Shop, o.ShopifyOrderID),
			Items: make([]itemResponse, len(o.Items)),
		}
		for j, item := range o.Items {
			response[i].Items[j] = itemResponse{
				ID: item.ID, Title: item.Title, SKU: item.Sku, PotSize: item.PotSize, Quantity: item.Quantity,
				PriceCents: item.PriceCents, InPlantStock: item.VariantID != nil, HeldBarcodes: item.HeldBarcodes,
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
