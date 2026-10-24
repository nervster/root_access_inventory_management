package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
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
      id name createdAt updatedAt cancelledAt displayFulfillmentStatus note
      customAttributes { key value }
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
	Note                     *string    `json:"note"` // the cart note: the shopper's note about pickup
	CustomAttributes         []struct {
		Key   string  `json:"key"`
		Value *string `json:"value"`
	} `json:"customAttributes"` // the cart's attributes, e.g. the pickup the theme saved
	LineItems struct {
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
	readReturns := canReadReturns(connection.Scopes)
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
			status, err := s.importOrder(ctx, orgID, shop, order)
			if err != nil {
				return fmt.Errorf("order %s: %w", order.Name, err)
			}
			if readReturns && status == "picked_up" { // only fulfilled orders can have returns
				if err := s.importReturns(ctx, orgID, shop, token, order.ID); err != nil {
					return fmt.Errorf("returns of %s: %w", order.Name, err)
				}
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

	if readReturns {
		// Unsettled returns, again: a refund may not mark its order changed.
		var active []string
		err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) (err error) {
			active, err = q.OrdersWithActiveReturns(ctx, orgID)
			return err
		})
		if err != nil {
			return err
		}
		for _, orderID := range active {
			if err := s.importReturns(ctx, orgID, shop, token, orderID); err != nil {
				return fmt.Errorf("returns of %s: %w", orderID, err)
			}
		}
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
// a problem with one order doesn't undo the others). It returns the order's status in Plant Stock
// ("" if Plant Stock doesn't keep it).
func (s *Store) importOrder(ctx context.Context, orgID int64, shop string, order shopifyOrder) (status string, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		existing, err := q.GetOrderByShopifyID(ctx, dbgen.GetOrderByShopifyIDParams{OrganizationID: orgID, ShopifyOrderID: order.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			if order.CancelledAt != nil || orderHandedOver(order.DisplayFulfillmentStatus) {
				return nil // over before Plant Stock saw it: nothing to hold
			}
			status = "open"
			orderID, err := s.createOrder(ctx, q, orgID, shop, order)
			if err != nil {
				return err
			}
			return savePickup(ctx, q, orgID, orderID, order)
		} else if err != nil {
			return err
		}
		status = existing.Status

		err = q.UpdateOrderFromShopify(ctx, dbgen.UpdateOrderFromShopifyParams{
			OrganizationID: orgID, ID: existing.ID, CancelledAt: order.CancelledAt,
			ShopifyFulfillmentStatus: &order.DisplayFulfillmentStatus, ShopifyUpdatedAt: order.UpdatedAt,
		})
		if err != nil || (existing.Status != "open" && existing.Status != "ready_for_pickup") {
			return err // picked up or cancelled already: nothing more to do
		}
		switch {
		case order.CancelledAt != nil:
			status = "cancelled"
			return cancelInPlantStock(ctx, q, orgID, existing.ID, order.Name)
		case orderHandedOver(order.DisplayFulfillmentStatus):
			sold, err := q.SellOrder(ctx, dbgen.SellOrderParams{OrganizationID: orgID, OrderID: existing.ID})
			if err != nil {
				return err
			}
			slog.Info("order fulfilled in Shopify; plants sold", "organization", orgID, "order", order.Name, "plants", len(sold))
			status = "picked_up"
			return q.SetOrderStatus(ctx, dbgen.SetOrderStatusParams{OrganizationID: orgID, ID: existing.ID, Status: "picked_up"})
		}
		// Still to collect: book its pickup if that didn't work before (the slot may be free now), and
		// keep its pickup details as Shopify has them.
		return savePickup(ctx, q, orgID, existing.ID, order)
	})
	return status, err
}

// savePickup books the pickup the order's cart chose (its hold, or the date and window, from the cart
// attributes) and records it on the order with the order note (the shopper's note), and a problem for
// staff if it couldn't be booked.
func savePickup(ctx context.Context, q *dbgen.Queries, orgID, orderID int64, order shopifyOrder) error {
	attributes := map[string]string{}
	for _, a := range order.CustomAttributes {
		if a.Value != nil {
			attributes[a.Key] = strings.TrimSpace(*a.Value)
		}
	}
	chosen := pickup.OrderPickup{HoldID: attributes["Pickup hold"], Window: attributes["Pickup window"]}
	if date, err := pickup.ParseDate(attributes["Pickup date"]); err == nil {
		chosen.Date = &date
	}
	var note *string
	if order.Note != nil {
		note = nonEmpty(strings.TrimSpace(*order.Note))
	}
	problem, err := pickup.BookOrder(ctx, q, orgID, orderID, chosen, time.Now())
	if err != nil {
		return err
	}
	if problem != "" {
		slog.Warn("order's pickup needs staff", "organization", orgID, "order", order.Name, "problem", problem)
	}
	// Once booked, the booking is the truth: the customer may have changed the time on the My pickup page
	// before Shopify's copy (the attributes) was updated.
	window := nonEmpty(attributes["Pickup window"])
	if booking, err := q.OrderPickupBooking(ctx, dbgen.OrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID}); err == nil {
		chosen.Date, window = &booking.Date, &booking.WindowName
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return q.SetOrderPickup(ctx, dbgen.SetOrderPickupParams{
		OrganizationID: orgID, ID: orderID, PickupDate: chosen.Date,
		PickupWindow: window, PickupNote: note, PickupProblem: nonEmpty(problem),
	})
}

func nonEmpty(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}

// ErrWebhookShop means a webhook's store (the X-Shopify-Shop-Domain header, which Shopify's signature
// doesn't cover) isn't the store its signed body names.
var ErrWebhookShop = errors.New("shopify: webhook's store doesn't match its body")

// checkWebhookShop makes sure the webhooks that change things act on the store their signed body names:
// only the body is signed, so a replayed webhook with another store's domain in the header must not
// uninstall or erase data for that store. Order webhooks only queue an import, so they're not checked.
func checkWebhookShop(topic, shop string, body []byte) error {
	var named struct {
		ShopDomain      string `json:"shop_domain"`      // customers/data_request, customers/redact, shop/redact
		MyshopifyDomain string `json:"myshopify_domain"` // app/uninstalled (the shop resource)
	}
	var signed string
	switch topic {
	case "customers/data_request", "customers/redact", "shop/redact":
		_ = json.Unmarshal(body, &named)
		signed = named.ShopDomain
	case "app/uninstalled":
		_ = json.Unmarshal(body, &named)
		signed = named.MyshopifyDomain
	default:
		return nil
	}
	if !strings.EqualFold(signed, shop) {
		return ErrWebhookShop
	}
	return nil
}

// cancelInPlantStock records a cancelled order: its held plants go back on sale and its pickup slot
// is freed. (Whoever cancelled it: in Shopify, or the customer on the My pickup page.)
func cancelInPlantStock(ctx context.Context, q *dbgen.Queries, orgID, orderID int64, name string) error {
	released, err := q.ReleaseOrder(ctx, dbgen.ReleaseOrderParams{OrganizationID: orgID, OrderID: orderID})
	if err != nil {
		return err
	}
	if err := pickup.CancelOrderBooking(ctx, q, orgID, orderID); err != nil {
		return err
	}
	slog.Info("order cancelled; plants back on sale", "organization", orgID, "order", name, "plants", len(released))
	return q.SetOrderStatus(ctx, dbgen.SetOrderStatusParams{OrganizationID: orgID, ID: orderID, Status: "cancelled"})
}

// orderHandedOver reports whether Shopify shows the order as fulfilled (picked up).
func orderHandedOver(fulfillmentStatus string) bool {
	return fulfillmentStatus == "FULFILLED"
}

func (s *Store) createOrder(ctx context.Context, q *dbgen.Queries, orgID int64, shop string, order shopifyOrder) (orderID int64, err error) {
	orderID, err = q.CreateOrder(ctx, dbgen.CreateOrderParams{
		OrganizationID: orgID, Shop: shop, ShopifyOrderID: order.ID, Name: order.Name, PlacedAt: order.CreatedAt,
		ShopifyFulfillmentStatus: &order.DisplayFulfillmentStatus, ShopifyUpdatedAt: order.UpdatedAt,
	})
	if err != nil {
		return 0, err
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
			return 0, err
		}

		itemID, err := q.CreateOrderItem(ctx, dbgen.CreateOrderItemParams{
			OrganizationID: orgID, OrderID: orderID, ShopifyLineItemID: item.ID, VariantID: variantID,
			Sku: item.SKU, Title: item.Title, Quantity: int32(item.Quantity), PriceCents: priceCents(item.OriginalUnitPriceSet.ShopMoney.Amount),
		})
		if err != nil {
			return 0, err
		}
		if variantID == nil {
			continue // not one of Plant Stock's SKUs (e.g. a product made in Shopify): nothing to hold
		}
		held, err := q.HoldPlants(ctx, dbgen.HoldPlantsParams{OrderItemID: &itemID, VariantID: *variantID, OrganizationID: orgID, Quantity: int32(item.Quantity)})
		if err != nil {
			return 0, err
		}
		if len(held) < item.Quantity {
			slog.Warn("order item oversold: not enough plants for sale", "organization", orgID, "order", order.Name,
				"sku", item.SKU, "wanted", item.Quantity, "held", len(held))
		}
	}
	slog.Info("order imported", "organization", orgID, "order", order.Name, "items", len(order.LineItems.Nodes))
	return orderID, nil
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
// uninstalled means connecting again; Shopify's privacy requests to delete customer data clear the
// notes customers wrote on returns and pickups (all Plant Stock keeps that a customer wrote; it has
// no names or contact details). customers/data_request needs nothing: the store already has those
// notes, in Shopify.
func (s *Store) HandleWebhook(ctx context.Context, topic, shop string, body []byte) error {
	if err := checkWebhookShop(topic, shop, body); err != nil {
		return err
	}
	if topic == "customers/redact" || topic == "shop/redact" {
		return s.redact(ctx, topic, shop, body)
	}
	orgID, err := dbgen.New(s.conn).ShopifyConnectionByShop(ctx, shop) // account-level: the nursery isn't known yet
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // a store no nursery is connected to (any more)
	} else if err != nil {
		return err
	}
	switch {
	case strings.HasPrefix(topic, "orders/"), strings.HasPrefix(topic, "returns/"), strings.HasPrefix(topic, "refunds/"):
		return s.QueueOrderImport(ctx, orgID)
	case topic == "app/uninstalled":
		slog.Info("Plant Stock was uninstalled from a store", "organization", orgID, "shop", shop)
		s.markNeedsReconnect(ctx, orgID)
	}
	return nil
}

// redact clears customers' return and pickup notes: for the orders a customers/redact request lists, or for all
// of the store's orders on shop/redact (sent 48 hours after a store uninstalls Plant Stock). Shopify
// sends customers/redact months after the customer's last order, when its returns are long settled,
// so a later import doesn't read the notes back.
func (s *Store) redact(ctx context.Context, topic, shop string, body []byte) error {
	var request struct {
		OrdersToRedact []int64 `json:"orders_to_redact"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return err
	}
	params := dbgen.RedactReturnNotesParams{Shop: shop, AllOrders: topic == "shop/redact"}
	for _, id := range request.OrdersToRedact {
		params.ShopifyOrderIds = append(params.ShopifyOrderIds, fmt.Sprintf("gid://shopify/Order/%d", id))
	}
	if !params.AllOrders && len(params.ShopifyOrderIds) == 0 {
		return nil
	}
	orgIDs, err := dbgen.New(s.conn).OrganizationsWithOrdersFrom(ctx, shop) // account-level, like ShopifyConnectionByShop
	if err != nil {
		return err
	}
	for _, orgID := range orgIDs {
		params.OrganizationID = orgID
		var cleared int64
		if err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
			returnNotes, err := q.RedactReturnNotes(ctx, params)
			if err != nil {
				return err
			}
			pickupNotes, err := q.RedactOrderPickupNotes(ctx, dbgen.RedactOrderPickupNotesParams(params))
			cleared = returnNotes + pickupNotes
			return err
		}); err != nil {
			return err
		}
		slog.Info("Shopify privacy request", "topic", topic, "organization", orgID, "shop", shop, "notes", cleared)
	}
	return nil
}

func (s *Store) fulfillOrderJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		OrderID int64 `json:"orderId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return s.FulfillOrder(ctx, job.OrganizationID, payload.OrderID)
}

const fulfillmentOrdersQuery = `query FulfillmentOrders($id: ID!) {
  order(id: $id) { fulfillmentOrders(first: 10) { nodes { id status } } }
}`

const fulfillMutation = `mutation Fulfill($fulfillment: FulfillmentInput!) {
  fulfillmentCreate(fulfillment: $fulfillment) {
    fulfillment { id }
    userErrors { field message }
  }
}`

// FulfillOrder finishes a picked-up order: it's fulfilled in Shopify (without emailing the
// customer), then its held plants are sold. In that order: Shopify subtracts fulfilled stock from on
// hand itself, so selling first would make the stock sync subtract the plants a second time. Already
// fulfilled in Shopify is fine; a failure is retried, the plants staying held meanwhile.
func (s *Store) FulfillOrder(ctx context.Context, orgID, orderID int64) error {
	var order dbgen.Order
	err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) (err error) {
		order, err = q.GetOrder(ctx, dbgen.GetOrderParams{OrganizationID: orgID, ID: orderID})
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if order.Status != "picked_up" {
		return nil // e.g. cancelled since
	}

	shop, token, err := s.accessToken(ctx, orgID)
	switch {
	case errors.Is(err, ErrNotConnected):
		// The store was disconnected: nothing to tell Shopify; just sell the plants.
	case err != nil:
		return err
	case shop != order.Shop:
		// Connected to a different store now; the order belongs to the old one.
	default:
		if err := s.fulfillOnShopify(ctx, shop, token, order); err != nil {
			return err
		}
	}

	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		sold, err := q.SellOrder(ctx, dbgen.SellOrderParams{OrganizationID: orgID, OrderID: orderID})
		slog.Info("order picked up; plants sold", "organization", orgID, "order", order.Name, "plants", len(sold))
		return err
	})
}

func (s *Store) fulfillOnShopify(ctx context.Context, shop, token string, order dbgen.Order) error {
	var data struct {
		Order *struct {
			FulfillmentOrders struct {
				Nodes []struct{ ID, Status string } `json:"nodes"`
			} `json:"fulfillmentOrders"`
		} `json:"order"`
	}
	if err := s.app.GraphQL(ctx, shop, token, fulfillmentOrdersQuery, map[string]any{"id": order.ShopifyOrderID}, &data); err != nil {
		return err
	}
	if data.Order == nil {
		return nil // deleted in Shopify
	}
	var open []map[string]string
	for _, fo := range data.Order.FulfillmentOrders.Nodes {
		if fo.Status == "OPEN" || fo.Status == "IN_PROGRESS" {
			open = append(open, map[string]string{"fulfillmentOrderId": fo.ID})
		}
	}
	if len(open) == 0 {
		return nil // fulfilled in Shopify already (or nothing to fulfill)
	}

	var result struct {
		FulfillmentCreate struct {
			Fulfillment *struct{ ID string }       `json:"fulfillment"`
			UserErrors  []struct{ Message string } `json:"userErrors"`
		} `json:"fulfillmentCreate"`
	}
	variables := map[string]any{"fulfillment": map[string]any{"lineItemsByFulfillmentOrder": open, "notifyCustomer": false}}
	if err := s.app.GraphQL(ctx, shop, token, fulfillMutation, variables, &result); err != nil {
		return err
	}
	if result.FulfillmentCreate.Fulfillment == nil {
		message := "no fulfillment returned"
		if len(result.FulfillmentCreate.UserErrors) > 0 {
			message = result.FulfillmentCreate.UserErrors[0].Message
		}
		return fmt.Errorf("shopify fulfill %s: %s", order.Name, message)
	}
	return nil
}
