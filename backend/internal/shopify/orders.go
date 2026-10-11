package shopify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// JobImportOrders reads a store's new and changed orders. The worker queues it for every connected
// store each minute (QueueOrderImports); Shopify's order webhooks, and "Check now", queue it sooner.
const JobImportOrders = "shopify.import_orders"

// orderPages is the most pages of 50 orders one import reads; the rest wait for the next one.
const orderPages = 10

// firstImportDays is how far back the first import looks for open orders (Shopify's limit
// without read_all_orders is 60 days).
const firstImportDays = 60

func (s *Store) importOrdersJob(ctx context.Context, job jobs.Job) error {
	return s.ImportOrders(ctx, job.OrganizationID)
}

// QueueOrderImports queues an order import for every connected store (the worker, each minute).
func QueueOrderImports(ctx context.Context, conn db.Conn) error {
	return dbgen.New(conn).QueueOrderImports(ctx)
}

// QueueOrderImport queues reading the store's orders now ("Check now").
func (s *Store) QueueOrderImport(ctx context.Context, orgID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetShopifyConnection(ctx, orgID); err != nil {
			return notConnected(err)
		}
		return jobs.Enqueue(ctx, q, orgID, JobImportOrders, struct{}{})
	})
}

const ordersQuery = `query Orders($query: String!, $after: String) {
  orders(first: 50, after: $after, sortKey: UPDATED_AT, query: $query) {
    nodes {
      id name createdAt updatedAt cancelledAt displayFulfillmentStatus
      lineItems(first: 100) {
        nodes { id title sku quantity variant { id } originalUnitPriceSet { shopMoney { amount } } }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

type shopifyOrder struct {
	ID                       string     `json:"id"`
	Name                     string     `json:"name"`
	CreatedAt                time.Time  `json:"createdAt"`
	UpdatedAt                time.Time  `json:"updatedAt"`
	CancelledAt              *time.Time `json:"cancelledAt"`
	DisplayFulfillmentStatus string     `json:"displayFulfillmentStatus"`
	LineItems                struct {
		Nodes []shopifyLineItem `json:"nodes"`
	} `json:"lineItems"`
}

type shopifyLineItem struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	SKU      *string `json:"sku"`
	Quantity int     `json:"quantity"`
	Variant  *struct {
		ID string `json:"id"`
	} `json:"variant"`
	OriginalUnitPriceSet struct {
		ShopMoney struct {
			Amount string `json:"amount"`
		} `json:"shopMoney"`
	} `json:"originalUnitPriceSet"`
}

// ImportOrders reads the store's orders changed since the last import: a new open order holds
// plants for each of its items; a cancelled one puts them back on sale; one fulfilled in Shopify
// (handed over without scanning) sells them. Importing an order again changes nothing more, so
// overlapping imports and repeated webhooks are safe.
//
// Shopify refusing the query (e.g. the app may not read orders yet) is recorded for Settings, not
// retried; the next minute's import tries again anyway.
func (s *Store) ImportOrders(ctx context.Context, orgID int64) error {
	connection, err := s.Connection(ctx, orgID)
	if errors.Is(err, ErrNotConnected) {
		return nil
	} else if err != nil {
		return err
	}
	shop, token, err := s.accessToken(ctx, orgID)
	if errors.Is(err, ErrReconnect) {
		return nil // Settings already asks the Owner to connect again
	} else if err != nil {
		return err
	}

	started := time.Now()
	search := "status:open AND fulfillment_status:unfulfilled AND created_at:>=" + started.AddDate(0, 0, -firstImportDays).UTC().Format("2006-01-02")
	if connection.OrdersCheckedThrough != nil {
		// A little overlap, in case Shopify's clock and ours differ: re-reading an order is harmless.
		search = "updated_at:>='" + connection.OrdersCheckedThrough.Add(-2*time.Minute).UTC().Format(time.RFC3339) + "'"
	}

	through := started
	var after *string
	for page := 0; ; page++ {
		var data struct {
			Orders struct {
				Nodes    []shopifyOrder `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"orders"`
		}
		err := s.app.GraphQL(ctx, shop, token, ordersQuery, map[string]any{"query": search, "after": after}, &data)
		if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrReconnect) {
			return err
		} else if err != nil {
			return s.recordOrdersError(ctx, orgID, err)
		}
		for _, order := range data.Orders.Nodes {
			if err := s.importOrder(ctx, orgID, shop, order); err != nil {
				return fmt.Errorf("order %s: %w", order.Name, err)
			}
		}

		if !data.Orders.PageInfo.HasNextPage {
			break
		}
		if page+1 == orderPages {
			// More than one import's worth: continue from the last one read (orders come oldest change first).
			nodes := data.Orders.Nodes
			through = nodes[len(nodes)-1].UpdatedAt
			if err := s.QueueOrderImport(ctx, orgID); err != nil {
				return err
			}
			break
		}
		after = &data.Orders.PageInfo.EndCursor
	}

	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		return q.SetOrdersChecked(ctx, dbgen.SetOrdersCheckedParams{OrganizationID: orgID, Through: &through})
	})
}

func (s *Store) recordOrdersError(ctx context.Context, orgID int64, cause error) error {
	message := strings.TrimPrefix(cause.Error(), "shopify graphql: ")
	slog.Warn("couldn't read Shopify orders", "organization", orgID, "error", message)
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		return q.SetOrdersError(ctx, dbgen.SetOrdersErrorParams{OrganizationID: orgID, OrdersError: &message})
	})
}

// importOrder records one order and holds, releases, or sells its plants (one transaction each, so
// a problem with one order doesn't undo the others).
func (s *Store) importOrder(ctx context.Context, orgID int64, shop string, order shopifyOrder) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		existing, err := q.GetOrderByShopifyID(ctx, dbgen.GetOrderByShopifyIDParams{OrganizationID: orgID, ShopifyOrderID: order.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			if order.CancelledAt != nil || orderHandedOver(order.DisplayFulfillmentStatus) {
				return nil // over before Plant Stock saw it: nothing to hold
			}
			return s.createOrder(ctx, q, orgID, shop, order)
		} else if err != nil {
			return err
		}

		err = q.UpdateOrderFromShopify(ctx, dbgen.UpdateOrderFromShopifyParams{
			OrganizationID: orgID, ID: existing.ID, CancelledAt: order.CancelledAt,
			ShopifyFulfillmentStatus: &order.DisplayFulfillmentStatus, ShopifyUpdatedAt: order.UpdatedAt,
		})
		if err != nil || existing.Status != "open" {
			return err
		}
		switch {
		case order.CancelledAt != nil:
			released, err := q.ReleaseOrder(ctx, dbgen.ReleaseOrderParams{OrganizationID: orgID, OrderID: existing.ID})
			if err != nil {
				return err
			}
			slog.Info("order cancelled; plants back on sale", "organization", orgID, "order", order.Name, "plants", len(released))
			return q.SetOrderStatus(ctx, dbgen.SetOrderStatusParams{OrganizationID: orgID, ID: existing.ID, Status: "cancelled"})
		case orderHandedOver(order.DisplayFulfillmentStatus):
			sold, err := q.SellOrder(ctx, dbgen.SellOrderParams{OrganizationID: orgID, OrderID: existing.ID})
			if err != nil {
				return err
			}
			slog.Info("order fulfilled in Shopify; plants sold", "organization", orgID, "order", order.Name, "plants", len(sold))
			return q.SetOrderStatus(ctx, dbgen.SetOrderStatusParams{OrganizationID: orgID, ID: existing.ID, Status: "picked_up"})
		}
		return nil
	})
}

// orderHandedOver reports whether Shopify shows the order as fulfilled (picked up).
func orderHandedOver(fulfillmentStatus string) bool {
	return fulfillmentStatus == "FULFILLED"
}

func (s *Store) createOrder(ctx context.Context, q *dbgen.Queries, orgID int64, shop string, order shopifyOrder) error {
	orderID, err := q.CreateOrder(ctx, dbgen.CreateOrderParams{
		OrganizationID: orgID, Shop: shop, ShopifyOrderID: order.ID, Name: order.Name, PlacedAt: order.CreatedAt,
		ShopifyFulfillmentStatus: &order.DisplayFulfillmentStatus, ShopifyUpdatedAt: order.UpdatedAt,
	})
	if err != nil {
		return err
	}
	for _, item := range order.LineItems.Nodes {
		var shopifyVariantID *string
		if item.Variant != nil {
			shopifyVariantID = &item.Variant.ID
		}
		var variantID *int64
		id, err := q.VariantForOrderItem(ctx, dbgen.VariantForOrderItemParams{Shop: shop, OrganizationID: orgID, ShopifyVariantID: shopifyVariantID, Sku: item.SKU})
		if err == nil {
			variantID = &id
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		itemID, err := q.CreateOrderItem(ctx, dbgen.CreateOrderItemParams{
			OrganizationID: orgID, OrderID: orderID, ShopifyLineItemID: item.ID, VariantID: variantID,
			Sku: item.SKU, Title: item.Title, Quantity: int32(item.Quantity), PriceCents: priceCents(item.OriginalUnitPriceSet.ShopMoney.Amount),
		})
		if err != nil {
			return err
		}
		if variantID == nil {
			continue // not one of Plant Stock's SKUs (e.g. a product made in Shopify): nothing to hold
		}
		held, err := q.HoldPlants(ctx, dbgen.HoldPlantsParams{OrderItemID: &itemID, VariantID: *variantID, OrganizationID: orgID, Quantity: int32(item.Quantity)})
		if err != nil {
			return err
		}
		if len(held) < item.Quantity {
			slog.Warn("order item oversold: not enough plants for sale", "organization", orgID, "order", order.Name,
				"sku", item.SKU, "wanted", item.Quantity, "held", len(held))
		}
	}
	slog.Info("order imported", "organization", orgID, "order", order.Name, "items", len(order.LineItems.Nodes))
	return nil
}

// priceCents turns Shopify's "15.00" into 1500 (nil if it isn't a price).
func priceCents(amount string) *int32 {
	dollars, cents, _ := strings.Cut(amount, ".")
	cents = (cents + "00")[:2]
	whole, err1 := strconv.Atoi(dollars)
	part, err2 := strconv.Atoi(cents)
	if err1 != nil || err2 != nil {
		return nil
	}
	total := int32(whole*100 + part)
	return &total
}

// HandleWebhook acts on a verified webhook from shop: order changes queue an import; the app being
// uninstalled means connecting again. Plant Stock keeps no customer data, so Shopify's privacy
// requests (customers/data_request, customers/redact, shop/redact) need nothing done.
func (s *Store) HandleWebhook(ctx context.Context, topic, shop string) error {
	orgID, err := dbgen.New(s.conn).ShopifyConnectionByShop(ctx, shop) // account-level: the nursery isn't known yet
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // a store no nursery is connected to (any more)
	} else if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(topic, "orders/"):
		return s.QueueOrderImport(ctx, orgID)
	case topic == "app/uninstalled":
		slog.Info("Plant Stock was uninstalled from a store", "organization", orgID, "shop", shop)
		s.markNeedsReconnect(ctx, orgID)
	}
	return nil
}
