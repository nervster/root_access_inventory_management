package shopify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// JobSyncStock sets a product's stock on the store. A trigger queues it when plants go on or off
// sale (migration 00018); a product sync queues it too, for new SKUs.
const JobSyncStock = "shopify.sync_stock"

func (s *Store) syncStockJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		ProductID int64 `json:"productId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return s.SyncStock(ctx, job.OrganizationID, payload.ProductID)
}

// SyncStock sets the on-hand count of each of the product's SKUs on the store, at the pickup
// location, to its number of for-sale plants.
//
// On hand, not "available": when a customer orders, Shopify moves a unit from available to
// committed, and on hand stays the same until pickup. So Shopify keeps subtracting orders Plant
// Stock hasn't heard about yet, and a sync arriving in between can't put a sold plant back on sale.
func (s *Store) SyncStock(ctx context.Context, orgID, productID int64) error {
	connection, err := s.Connection(ctx, orgID)
	if errors.Is(err, ErrNotConnected) {
		return nil
	} else if err != nil {
		return err
	}
	if connection.LocationID == nil {
		return nil // no pickup location chosen yet; Settings asks for one, and choosing it syncs everything
	}
	location := *connection.LocationID
	shop, token, err := s.accessToken(ctx, orgID)
	if err != nil {
		return err
	}

	// A failure that needs a retry, reported after the transaction commits what it learned.
	var retry error
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		// The product sync's lock: it may be replacing these SKUs on the store right now.
		if err := q.LockProductSync(ctx, productID); err != nil {
			return err
		}
		skus, err := q.StockToSync(ctx, dbgen.StockToSyncParams{Shop: shop, OrganizationID: orgID, ProductID: productID})
		if err != nil || len(skus) == 0 {
			return err // none on the store (yet)
		}
		stock := stockSync{q: q, app: s.app, shop: shop, token: token, location: location, productID: productID}
		retry, err = stock.run(ctx, orgID, skus)
		return err
	})
	if err != nil {
		return err
	}
	return retry
}

type stockSync struct {
	q         *dbgen.Queries
	app       *App
	shop      string
	token     string
	location  string
	productID int64
}

// run sets the stock. retry is a failure to try again after committing (err rolls back instead).
func (st stockSync) run(ctx context.Context, orgID int64, skus []dbgen.StockToSyncRow) (retry, err error) {
	var quantities []map[string]any
	var newlyStocked []int64
	for _, sku := range skus {
		if sku.StockedLocationID == nil || *sku.StockedLocationID != st.location {
			if err := st.activate(ctx, sku.ShopifyInventoryItemID); err != nil {
				return nil, err
			}
			newlyStocked = append(newlyStocked, sku.ID)
			if sku.StockedLocationID != nil {
				// The pickup location changed: nothing is for sale at the old one any more.
				quantities = append(quantities, quantity(sku.ShopifyInventoryItemID, *sku.StockedLocationID, 0))
			}
		}
		quantities = append(quantities, quantity(sku.ShopifyInventoryItemID, st.location, int(sku.Stock)))
	}
	if len(newlyStocked) > 0 {
		err := st.q.SetShopifyVariantStockedAt(ctx, dbgen.SetShopifyVariantStockedAtParams{LocationID: &st.location, Shop: st.shop, VariantIds: newlyStocked})
		if err != nil {
			return nil, err
		}
	}

	userErrors, err := st.setOnHand(ctx, quantities)
	if err != nil {
		return nil, err
	}
	for _, e := range userErrors {
		switch e.Code {
		case "ITEM_NOT_STOCKED_AT_LOCATION":
			// Someone removed it from the location in Shopify: stock it again on the retry.
			ids := make([]int64, len(skus))
			for i, sku := range skus {
				ids[i] = sku.ID
			}
			if err := st.q.SetShopifyVariantStockedAt(ctx, dbgen.SetShopifyVariantStockedAtParams{Shop: st.shop, VariantIds: ids}); err != nil {
				return nil, err
			}
			return fmt.Errorf("shopify stock: %s", e.Message), nil
		case "INVALID_INVENTORY_ITEM":
			// The SKU was deleted on the store: the product sync creates it again (then syncs stock).
			slog.Info("an SKU was deleted on Shopify; syncing its product again", "product", st.productID)
			return nil, jobs.Enqueue(ctx, st.q, orgID, JobSyncProduct, map[string]int64{"productId": st.productID})
		}
	}
	if len(userErrors) > 0 {
		return fmt.Errorf("shopify stock: %s (%s)", userErrors[0].Message, userErrors[0].Code), nil
	}
	return nil, nil
}

func quantity(inventoryItemID, locationID string, n int) map[string]any {
	return map[string]any{
		"inventoryItemId": inventoryItemID, "locationId": locationID, "quantity": n,
		"changeFromQuantity": nil, // Plant Stock is the record: set it whatever Shopify had
	}
}

const activateMutation = `mutation StockAtLocation($inventoryItemId: ID!, $locationId: ID!, $idempotencyKey: String!) {
  inventoryActivate(inventoryItemId: $inventoryItemId, locationId: $locationId) @idempotent(key: $idempotencyKey) {
    inventoryLevel { id }
    userErrors { field message }
  }
}`

// activate stocks an SKU at the pickup location, so its count can be set there.
func (st stockSync) activate(ctx context.Context, inventoryItemID string) error {
	var data struct {
		InventoryActivate struct {
			InventoryLevel *struct{ ID string }       `json:"inventoryLevel"`
			UserErrors     []struct{ Message string } `json:"userErrors"`
		} `json:"inventoryActivate"`
	}
	variables := map[string]any{"inventoryItemId": inventoryItemID, "locationId": st.location, "idempotencyKey": idempotencyKey()}
	if err := st.app.GraphQL(ctx, st.shop, st.token, activateMutation, variables, &data); err != nil {
		return err
	}
	if data.InventoryActivate.InventoryLevel == nil {
		message := "no inventory level returned"
		if len(data.InventoryActivate.UserErrors) > 0 {
			message = data.InventoryActivate.UserErrors[0].Message
		}
		return fmt.Errorf("shopify stock at location: %s", message)
	}
	return nil
}

const setOnHandMutation = `mutation SetStock($input: InventorySetQuantitiesInput!, $idempotencyKey: String!) {
  inventorySetQuantities(input: $input) @idempotent(key: $idempotencyKey) {
    inventoryAdjustmentGroup { id }
    userErrors { field message code }
  }
}`

type inventoryUserError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

func (st stockSync) setOnHand(ctx context.Context, quantities []map[string]any) ([]inventoryUserError, error) {
	var data struct {
		InventorySetQuantities struct {
			UserErrors []inventoryUserError `json:"userErrors"`
		} `json:"inventorySetQuantities"`
	}
	variables := map[string]any{
		"idempotencyKey": idempotencyKey(),
		"input": map[string]any{
			"name":       "on_hand",
			"reason":     "correction",
			"quantities": quantities,
			// Shown in Shopify's inventory history as where the change came from.
			"referenceDocumentUri": fmt.Sprintf("gid://plant-stock/Product/%d", st.productID),
		},
	}
	err := st.app.GraphQL(ctx, st.shop, st.token, setOnHandMutation, variables, &data)
	return data.InventorySetQuantities.UserErrors, err
}

// idempotencyKey lets Shopify recognize a repeated request (e.g. after a timeout) and not apply it
// twice. Each call is a new request, so each gets a new key.
func idempotencyKey() string {
	key := make([]byte, 16)
	_, _ = rand.Read(key)
	return hex.EncodeToString(key)
}
