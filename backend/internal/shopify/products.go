package shopify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// Job kinds. A database trigger queues sync_product whenever a product, its variants, or a pot
// size changes (migration 00017); connecting a store, or the Owner's "Sync now", queues sync_all.
const (
	JobSyncProduct = "shopify.sync_product"
	JobSyncAll     = "shopify.sync_all"
)

// potSizeOption is the name of the option a product's variants differ by on Shopify.
const potSizeOption = "Pot size"

// JobHandlers are the Shopify jobs the worker runs.
func (s *Store) JobHandlers() map[string]jobs.Handler {
	return map[string]jobs.Handler{
		JobSyncProduct:  s.syncProductJob,
		JobSyncAll:      s.syncAllJob,
		JobSyncStock:    s.syncStockJob,
		JobImportOrders: s.importOrdersJob,
	}
}

func (s *Store) syncProductJob(ctx context.Context, job jobs.Job) error {
	var payload struct {
		ProductID int64 `json:"productId"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	return s.SyncProduct(ctx, job.OrganizationID, payload.ProductID)
}

// syncAllJob queues a sync of every product worth pushing: priced, or already on the store.
func (s *Store) syncAllJob(ctx context.Context, job jobs.Job) error {
	connection, err := s.Connection(ctx, job.OrganizationID)
	if errors.Is(err, ErrNotConnected) {
		return nil // disconnected since
	} else if err != nil {
		return err
	}
	return db.InTenant(ctx, s.conn, job.OrganizationID, func(q *dbgen.Queries) error {
		ids, err := q.ProductsToSync(ctx, dbgen.ProductsToSyncParams{OrganizationID: job.OrganizationID, Shop: connection.Shop})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := jobs.Enqueue(ctx, q, job.OrganizationID, JobSyncProduct, map[string]int64{"productId": id}); err != nil {
				return err
			}
		}
		slog.Info("queued Shopify product syncs", "organization", job.OrganizationID, "products", len(ids))
		return nil
	})
}

// QueueSyncAll queues pushing every product to the store (the Owner's "Sync now").
func (s *Store) QueueSyncAll(ctx context.Context, orgID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetShopifyConnection(ctx, orgID); err != nil {
			return notConnected(err)
		}
		return jobs.Enqueue(ctx, q, orgID, JobSyncAll, struct{}{})
	})
}

// SyncProduct makes the store's copy of a product match it: title, description, type, tags, and
// one variant per priced pot size (SKU and price). Unpriced variants stay off the store. A product
// new to the store is created as a draft, for the Owner to check and publish; an existing one keeps
// whatever status it has, except that one with no priced variants left goes back to draft.
//
// Shopify refusing the product (e.g. an invalid value) is recorded on it for Settings to show, and
// not retried; temporary problems return an error, so the job is retried.
func (s *Store) SyncProduct(ctx context.Context, orgID, productID int64) error {
	shop, token, err := s.accessToken(ctx, orgID)
	if errors.Is(err, ErrNotConnected) {
		return nil // disconnected since the job was queued
	} else if err != nil {
		return err
	}

	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		// Held until this transaction ends, through the call to Shopify: two syncs of a new product
		// at once would otherwise both create it.
		if err := q.LockProductSync(ctx, productID); err != nil {
			return err
		}
		product, err := q.ProductToSync(ctx, dbgen.ProductToSyncParams{Shop: shop, OrganizationID: orgID, ProductID: productID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // deleted since
		} else if err != nil {
			return err
		}
		variants, err := q.VariantsToSync(ctx, dbgen.VariantsToSyncParams{Shop: shop, OrganizationID: orgID, ProductID: productID})
		if err != nil {
			return err
		}

		images, err := q.PhotosToSync(ctx, dbgen.PhotosToSyncParams{Shop: shop, OrganizationID: orgID, ProductID: productID})
		if err != nil {
			return err
		}
		hasMedia, err := q.HasShopifyMedia(ctx, dbgen.HasShopifyMediaParams{ProductID: productID, Shop: shop})
		if err != nil {
			return err
		}

		sync := productSync{q: q, app: s.app, photos: s.photos, orgID: orgID, shop: shop, token: token,
			product: product, variants: variants, images: images, hasMedia: hasMedia}
		return sync.run(ctx)
	})
}

// productSync is one product's sync, inside SyncProduct's transaction.
type productSync struct {
	q        *dbgen.Queries
	app      *App
	orgID    int64
	shop     string
	token    string
	photos   PhotoSource
	product  dbgen.ProductToSyncRow
	variants []dbgen.VariantsToSyncRow
	images   []dbgen.PhotosToSyncRow // the chosen photos, in order
	hasMedia bool                    // Plant Stock has put images on the store's copy before
}

func (p productSync) run(ctx context.Context) error {
	var priced []dbgen.VariantsToSyncRow
	for _, v := range p.variants {
		if v.PriceCents != nil {
			priced = append(priced, v)
		}
	}
	onStore := p.product.ShopifyProductID != nil
	if len(priced) == 0 && !onStore {
		return nil // nothing to sell yet
	}

	if len(priced) == 0 {
		return p.takeOffSale(ctx) // every SKU lost its price
	}
	files, sendFiles, err := p.files(ctx, false)
	if err != nil {
		return err
	}
	result, err := p.productSet(ctx, p.productInput(priced, !onStore, files, sendFiles))

	switch {
	case err != nil:
	case result.hasError("NOT_FOUND", "PRODUCT_DOES_NOT_EXIST"):
		// Deleted on the store (by someone in Shopify): Plant Stock is the record, so create it again.
		slog.Info("product was deleted on Shopify; creating it again", "organization", p.orgID, "product", p.product.ID)
		p.product.ShopifyProductID = nil
		for i := range priced {
			priced[i].ShopifyVariantID = nil
		}
		if files, sendFiles, err = p.files(ctx, true); err != nil {
			return err
		}
		result, err = p.productSet(ctx, p.productInput(priced, true, files, sendFiles))
	case result.hasError("PRODUCT_VARIANT_DOES_NOT_EXIST"):
		// A variant was deleted on the store: send them all as new.
		for i := range priced {
			priced[i].ShopifyVariantID = nil
		}
		result, err = p.productSet(ctx, p.productInput(priced, false, files, sendFiles))
	case result.hasFieldError("files"):
		// An image was deleted on the store: upload them all again.
		slog.Info("an image was deleted on Shopify; uploading the product's images again", "organization", p.orgID, "product", p.product.ID)
		if files, sendFiles, err = p.files(ctx, true); err != nil {
			return err
		}
		result, err = p.productSet(ctx, p.productInput(priced, false, files, sendFiles))
	}
	if err != nil {
		return err
	}
	if len(result.UserErrors) > 0 {
		message := result.UserErrors[0].Message
		slog.Warn("Shopify refused a product", "organization", p.orgID, "product", p.product.ID, "error", message)
		return p.q.SaveShopifySyncError(ctx, dbgen.SaveShopifySyncErrorParams{
			OrganizationID: p.orgID, ProductID: p.product.ID, Shop: p.shop, SyncError: &message,
		})
	}
	return p.save(ctx, result, sendFiles)
}

const takeOffSaleMutation = `mutation TakeOffSale($product: ProductUpdateInput!) {
  productUpdate(product: $product) {
    product { id }
    userErrors { field message }
  }
}`

// takeOffSale puts the store's copy back to draft, leaving the rest of it as it is.
func (p productSync) takeOffSale(ctx context.Context) error {
	var data struct {
		ProductUpdate struct {
			Product    *struct{ ID string }       `json:"product"`
			UserErrors []struct{ Message string } `json:"userErrors"`
		} `json:"productUpdate"`
	}
	variables := map[string]any{"product": map[string]string{"id": *p.product.ShopifyProductID, "status": "DRAFT"}}
	if err := p.app.GraphQL(ctx, p.shop, p.token, takeOffSaleMutation, variables, &data); err != nil {
		return err
	}
	if data.ProductUpdate.Product == nil {
		// Gone from the store already, which is what's wanted; forget it.
		if err := p.q.ForgetShopifyVariants(ctx, dbgen.ForgetShopifyVariantsParams{Shop: p.shop, ProductID: p.product.ID}); err != nil {
			return err
		}
		return p.q.ForgetShopifyProduct(ctx, dbgen.ForgetShopifyProductParams{ProductID: p.product.ID, Shop: p.shop})
	}
	return p.q.SaveShopifyProduct(ctx, dbgen.SaveShopifyProductParams{
		OrganizationID: p.orgID, ProductID: p.product.ID, Shop: p.shop, ShopifyProductID: p.product.ShopifyProductID,
	})
}

// productInput is the store's copy of the product. Variants are matched by their Shopify IDs;
// ones left out are deleted from the store.
func (p productSync) productInput(priced []dbgen.VariantsToSyncRow, create bool, files []map[string]any, sendFiles bool) map[string]any {
	potSizes := make([]map[string]string, len(priced))
	variants := make([]map[string]any, len(priced))
	for i, v := range priced {
		potSizes[i] = map[string]string{"name": v.PotSize}
		variant := map[string]any{
			"optionValues":  []map[string]string{{"optionName": potSizeOption, "name": v.PotSize}},
			"price":         fmt.Sprintf("%d.%02d", *v.PriceCents/100, *v.PriceCents%100),
			"inventoryItem": map[string]any{"sku": v.Sku, "tracked": true}, // stock comes from Plant Stock
		}
		if v.ShopifyVariantID != nil {
			variant["id"] = *v.ShopifyVariantID
		}
		variants[i] = variant
	}

	input := map[string]any{
		"title":           p.product.Title,
		"descriptionHtml": descriptionHTML(p.product.Description),
		"productType":     deref(p.product.ProductType),
		"tags":            p.product.Tags,
		"productOptions":  []map[string]any{{"name": potSizeOption, "position": 1, "values": potSizes}},
		"variants":        variants,
	}
	if create {
		input["status"] = "DRAFT" // the Owner checks it and publishes it in Shopify
	}
	if sendFiles {
		input["files"] = files // the images, in order; ones left out are removed from the store
	}
	return input
}

const productSetMutation = `mutation SyncProduct($input: ProductSetInput!, $identifier: ProductSetIdentifiers) {
  productSet(input: $input, identifier: $identifier, synchronous: true) {
    product {
      id
      variants(first: 250) { nodes { id sku inventoryItem { id } } }
      media(first: 250) { nodes { id } }
    }
    userErrors { field message code }
  }
}`

type productSetResult struct {
	Product *struct {
		ID       string `json:"id"`
		Variants struct {
			Nodes []struct {
				ID            string `json:"id"`
				SKU           string `json:"sku"`
				InventoryItem struct {
					ID string `json:"id"`
				} `json:"inventoryItem"`
			} `json:"nodes"`
		} `json:"variants"`
		Media struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"media"`
	} `json:"product"`
	UserErrors []struct {
		Field   []string `json:"field"`
		Message string   `json:"message"`
		Code    string   `json:"code"`
	} `json:"userErrors"`
}

// hasFieldError reports whether Shopify refused something under input.<field>.
func (r productSetResult) hasFieldError(field string) bool {
	for _, e := range r.UserErrors {
		if slices.Contains(e.Field, field) {
			return true
		}
	}
	return false
}

func (r productSetResult) hasError(codes ...string) bool {
	for _, e := range r.UserErrors {
		for _, code := range codes {
			if e.Code == code {
				return true
			}
		}
	}
	return false
}

func (p productSync) productSet(ctx context.Context, input map[string]any) (productSetResult, error) {
	variables := map[string]any{"input": input}
	if p.product.ShopifyProductID != nil {
		variables["identifier"] = map[string]string{"id": *p.product.ShopifyProductID}
	}
	var data struct {
		ProductSet productSetResult `json:"productSet"`
	}
	err := p.app.GraphQL(ctx, p.shop, p.token, productSetMutation, variables, &data)
	return data.ProductSet, err
}

// save records which Shopify product and variants the product's are, matching variants by SKU.
func (p productSync) save(ctx context.Context, result productSetResult, filesSent bool) error {
	if result.Product == nil {
		return errors.New("shopify productSet returned no product")
	}
	err := p.q.SaveShopifyProduct(ctx, dbgen.SaveShopifyProductParams{
		OrganizationID: p.orgID, ProductID: p.product.ID, Shop: p.shop, ShopifyProductID: &result.Product.ID,
	})
	if err != nil {
		return err
	}

	bySKU := map[string]int64{}
	for _, v := range p.variants {
		bySKU[v.Sku] = v.ID
	}
	keep := []int64{}
	for _, node := range result.Product.Variants.Nodes {
		variantID, ok := bySKU[node.SKU]
		if !ok {
			continue
		}
		keep = append(keep, variantID)
		err := p.q.SaveShopifyVariant(ctx, dbgen.SaveShopifyVariantParams{
			OrganizationID: p.orgID, VariantID: variantID, Shop: p.shop,
			ShopifyVariantID: node.ID, ShopifyInventoryItemID: node.InventoryItem.ID,
		})
		if err != nil {
			return err
		}
	}
	if err := p.q.ForgetOtherShopifyVariants(ctx, dbgen.ForgetOtherShopifyVariantsParams{Shop: p.shop, ProductID: p.product.ID, Keep: keep}); err != nil {
		return err
	}
	if filesSent {
		mediaIDs := make([]string, len(result.Product.Media.Nodes))
		for i, node := range result.Product.Media.Nodes {
			mediaIDs[i] = node.ID
		}
		if err := p.saveMedia(ctx, mediaIDs); err != nil {
			return err
		}
	}
	// New SKUs start with no stock on the store; this sets it.
	return jobs.Enqueue(ctx, p.q, p.orgID, JobSyncStock, map[string]int64{"productId": p.product.ID})
}

// descriptionHTML turns the plain-text description into Shopify's HTML: a paragraph per blank-line
// separated block, line breaks kept, and nothing the nursery typed read as HTML.
func descriptionHTML(description *string) string {
	text := strings.TrimSpace(strings.ReplaceAll(deref(description), "\r\n", "\n"))
	if text == "" {
		return ""
	}
	var out strings.Builder
	for _, paragraph := range strings.Split(text, "\n\n") {
		if paragraph = strings.TrimSpace(paragraph); paragraph != "" {
			out.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(paragraph), "\n", "<br>") + "</p>")
		}
	}
	return out.String()
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ProductStatus is how one product stands on the nursery's store.
type ProductStatus struct {
	dbgen.ListShopifyProductsRow
	Shop string
}

// ProductStatuses lists the products sent to the store (or refused by it), problems first.
func (s *Store) ProductStatuses(ctx context.Context, orgID int64) (statuses []ProductStatus, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		connection, err := q.GetShopifyConnection(ctx, orgID)
		if err != nil {
			return notConnected(err)
		}
		rows, err := q.ListShopifyProducts(ctx, dbgen.ListShopifyProductsParams{OrganizationID: orgID, Shop: connection.Shop})
		for _, row := range rows {
			statuses = append(statuses, ProductStatus{ListShopifyProductsRow: row, Shop: connection.Shop})
		}
		return err
	})
	return statuses, err
}
