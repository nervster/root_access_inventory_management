package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// The nightly drift check: Plant Stock is the record, so each night the worker compares each store
// with it, product by product, and queues the usual syncs to put any difference right: someone
// edited a price or stock in Shopify, deleted a product there, or a sync gave up.

// JobCheckDrift compares one store with Plant Stock. The worker queues it at 3 am in the nursery's
// time zone (QueueDriftChecks); Settings' "Check now" queues it too.
const JobCheckDrift = "shopify.check_drift"

// driftBatch is how many products one query reads (kept small: Shopify limits each query's cost).
const driftBatch = 10

// maxDifferences is how many differences a check records for Settings; it fixes them all.
const maxDifferences = 50

// QueueDriftChecks queues tonight's checks (the worker, every few minutes).
func QueueDriftChecks(ctx context.Context, conn db.Conn) error {
	return dbgen.New(conn).QueueDriftChecks(ctx)
}

// QueueDriftCheck queues a check now ("Check now").
func (s *Store) QueueDriftCheck(ctx context.Context, orgID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetShopifyConnection(ctx, orgID); err != nil {
			return notConnected(err)
		}
		return jobs.Enqueue(ctx, q, orgID, JobCheckDrift, struct{}{})
	})
}

func (s *Store) checkDriftJob(ctx context.Context, job jobs.Job) error {
	return s.CheckDrift(ctx, job.OrganizationID)
}

const driftQuery = `query DriftCheck($ids: [ID!]!, $location: ID!) {
  nodes(ids: $ids) {
    ... on Product {
      id title
      variants(first: 20) {
        nodes {
          sku price
          inventoryItem { inventoryLevel(locationId: $location) { quantities(names: ["on_hand"]) { quantity } } }
        }
      }
    }
  }
}`

type driftProduct struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Variants struct {
		Nodes []struct {
			SKU           string `json:"sku"`
			Price         string `json:"price"`
			InventoryItem struct {
				InventoryLevel *struct {
					Quantities []struct {
						Quantity int `json:"quantity"`
					} `json:"quantities"`
				} `json:"inventoryLevel"`
			} `json:"inventoryItem"`
		} `json:"nodes"`
	} `json:"variants"`
}

// expectedProduct is a product as the store should show it.
type expectedProduct struct {
	id               int64
	title            string
	shopifyProductID *string
	skus             []dbgen.DriftExpectedRow
}

// CheckDrift compares the store with Plant Stock and queues syncs for what differs: a product sync
// for a missing product, or a different title, set of SKUs, or price; a stock sync for a different
// on-hand count. It records what it found (in words) for Settings.
func (s *Store) CheckDrift(ctx context.Context, orgID int64) error {
	connection, err := s.Connection(ctx, orgID)
	if errors.Is(err, ErrNotConnected) {
		return nil
	} else if err != nil {
		return err
	}
	shop, token, err := s.accessToken(ctx, orgID)
	if errors.Is(err, ErrReconnect) {
		return nil
	} else if err != nil {
		return err
	}

	var products []*expectedProduct
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if err := q.StartDriftCheck(ctx, orgID); err != nil {
			return err
		}
		rows, err := q.DriftExpected(ctx, dbgen.DriftExpectedParams{Shop: shop, OrganizationID: orgID})
		for _, row := range rows {
			if len(products) == 0 || products[len(products)-1].id != row.ProductID {
				products = append(products, &expectedProduct{id: row.ProductID, title: row.Title, shopifyProductID: row.ShopifyProductID})
			}
			last := products[len(products)-1]
			last.skus = append(last.skus, row)
		}
		return err
	})
	if err != nil {
		return err
	}

	var differences []string
	syncProduct, syncStock := map[int64]bool{}, map[int64]bool{}
	for batch := range slices.Chunk(products, driftBatch) {
		var ids []string
		for _, p := range batch {
			if p.shopifyProductID != nil {
				ids = append(ids, *p.shopifyProductID)
			}
		}
		onStore := map[string]driftProduct{}
		if len(ids) > 0 {
			var data struct {
				Nodes []*driftProduct `json:"nodes"`
			}
			location := ""
			if connection.LocationID != nil {
				location = *connection.LocationID
			}
			if err := s.app.GraphQL(ctx, shop, token, driftQuery, map[string]any{"ids": ids, "location": location}, &data); err != nil {
				return err
			}
			for _, node := range data.Nodes {
				if node != nil {
					onStore[node.ID] = *node
				}
			}
		}
		for _, p := range batch {
			found, product, stock := compareProduct(p, onStore, connection.LocationID)
			differences = append(differences, found...)
			syncProduct[p.id] = syncProduct[p.id] || product
			syncStock[p.id] = syncStock[p.id] || stock
		}
	}

	if len(differences) > 0 {
		slog.Info("drift check found differences; fixing them", "organization", orgID, "count", len(differences))
	}
	recorded := differences
	if len(recorded) > maxDifferences {
		recorded = append(slices.Clone(recorded[:maxDifferences]), fmt.Sprintf("…and %d more", len(differences)-maxDifferences))
	}
	encoded, err := json.Marshal(append([]string{}, recorded...))
	if err != nil {
		return err
	}
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		for _, p := range products {
			if syncProduct[p.id] { // a product sync is followed by a stock sync anyway
				if err := jobs.Enqueue(ctx, q, orgID, JobSyncProduct, map[string]int64{"productId": p.id}); err != nil {
					return err
				}
			} else if syncStock[p.id] {
				if err := jobs.Enqueue(ctx, q, orgID, JobSyncStock, map[string]int64{"productId": p.id}); err != nil {
					return err
				}
			}
		}
		return q.SaveDriftDifferences(ctx, dbgen.SaveDriftDifferencesParams{OrganizationID: orgID, Differences: encoded})
	})
}

// compareProduct says how the store's copy of p differs from it, and whether a product or a stock
// sync puts that right.
func compareProduct(p *expectedProduct, onStore map[string]driftProduct, location *string) (differences []string, syncProduct, syncStock bool) {
	var priced []dbgen.DriftExpectedRow
	for _, sku := range p.skus {
		if sku.PriceCents != nil {
			priced = append(priced, sku)
		}
	}
	if p.shopifyProductID == nil {
		return []string{p.title + ": not on Shopify yet"}, true, false
	}
	product, ok := onStore[*p.shopifyProductID]
	if !ok {
		if len(priced) == 0 {
			return nil, false, false // gone from the store, and nothing to sell: fine
		}
		return []string{p.title + ": deleted on Shopify (it will be added back as a draft)"}, true, false
	}
	if len(priced) == 0 {
		return nil, false, false // off sale: the product sync already set it to draft
	}

	// A difference in the product itself needs a product sync; one in stock, a stock sync.
	productDiff := func(format string, args ...any) {
		differences = append(differences, fmt.Sprintf(format, args...))
		syncProduct = true
	}
	if product.Title != p.title {
		productDiff("%s: title on Shopify is %q", p.title, product.Title)
	}
	type variant struct {
		price  string
		onHand *int
	}
	variants := map[string]variant{}
	for _, v := range product.Variants.Nodes {
		var onHand *int
		if level := v.InventoryItem.InventoryLevel; level != nil && len(level.Quantities) > 0 {
			onHand = &level.Quantities[0].Quantity
		}
		variants[v.SKU] = variant{price: v.Price, onHand: onHand}
	}
	for _, sku := range priced {
		v, ok := variants[sku.Sku]
		if !ok {
			productDiff("%s: SKU %s is missing on Shopify", p.title, sku.Sku)
			continue
		}
		delete(variants, sku.Sku)
		if want := formatPrice(*sku.PriceCents); !samePrice(v.price, want) {
			productDiff("%s: %s costs $%s on Shopify, $%s here", p.title, sku.Sku, v.price, want)
		}
		// Stock is compared only where Plant Stock keeps it: at the pickup location, once stocked there.
		if location != nil && sku.StockedLocationID != nil && *sku.StockedLocationID == *location {
			if v.onHand == nil || *v.onHand != int(sku.Stock) {
				shopify := "none"
				if v.onHand != nil {
					shopify = fmt.Sprint(*v.onHand)
				}
				differences = append(differences, fmt.Sprintf("%s: %s has %s on hand on Shopify, %d here", p.title, sku.Sku, shopify, sku.Stock))
				syncStock = true
			}
		}
	}
	for sku := range variants {
		productDiff("%s: SKU %s is on Shopify but not for sale here", p.title, orNone(sku))
	}
	return differences, syncProduct, syncStock
}

func formatPrice(cents int32) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

// samePrice compares Shopify's "20.0" or "20.00" with "20.00".
func samePrice(shopify, want string) bool {
	cents := priceCents(shopify)
	return cents != nil && formatPrice(*cents) == want
}

func orNone(sku string) string {
	if sku == "" {
		return "(no SKU)"
	}
	return sku
}
