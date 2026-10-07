package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrSKUTaken        = errors.New("that SKU is already used")
	ErrVariantHasStock = errors.New("plants are for sale in that pot size")
)

// ProductWithVariants is a product with its variants (one per pot size it's sold in), smallest pot
// first, each with its live stock, and its images in order.
type ProductWithVariants struct {
	dbgen.ListProductsRow
	Variants []dbgen.ListVariantsWithStockRow
	Images   []dbgen.ListProductPhotosRow
}

// Products lists orgID's products by title, with their variants and stock.
func (s *Store) Products(ctx context.Context, orgID int64) (products []ProductWithVariants, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		rows, err := q.ListProducts(ctx, orgID)
		if err != nil {
			return err
		}
		variants, err := q.ListVariantsWithStock(ctx, dbgen.ListVariantsWithStockParams{OrganizationID: orgID})
		if err != nil {
			return err
		}
		images, err := q.ListProductPhotos(ctx, dbgen.ListProductPhotosParams{OrganizationID: orgID})
		if err != nil {
			return err
		}
		variantsOf := map[int64][]dbgen.ListVariantsWithStockRow{}
		for _, v := range variants {
			variantsOf[v.ProductID] = append(variantsOf[v.ProductID], v)
		}
		imagesOf := map[int64][]dbgen.ListProductPhotosRow{}
		for _, image := range images {
			imagesOf[image.ProductID] = append(imagesOf[image.ProductID], image)
		}
		products = make([]ProductWithVariants, len(rows))
		for i, row := range rows {
			products[i] = ProductWithVariants{ListProductsRow: row, Variants: variantsOf[row.ID], Images: imagesOf[row.ID]}
		}
		return nil
	})
	return products, err
}

// Product returns one product with its variants and stock, or ErrNotFound.
func (s *Store) Product(ctx context.Context, orgID, productID int64) (product ProductWithVariants, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		row, err := q.GetProduct(ctx, dbgen.GetProductParams{OrganizationID: orgID, ID: productID})
		if err != nil {
			return translate(err)
		}
		variants, err := q.ListVariantsWithStock(ctx, dbgen.ListVariantsWithStockParams{OrganizationID: orgID, ProductID: &productID})
		if err != nil {
			return err
		}
		images, err := q.ListProductPhotos(ctx, dbgen.ListProductPhotosParams{OrganizationID: orgID, ProductID: &productID})
		product = ProductWithVariants{ListProductsRow: dbgen.ListProductsRow(row), Variants: variants, Images: images}
		return err
	})
	return product, err
}

// UpdateProduct changes the title or description (nil keeps a value). It fails with ErrNotFound.
func (s *Store) UpdateProduct(ctx context.Context, params dbgen.UpdateProductParams) error {
	return db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		_, err := q.UpdateProduct(ctx, params)
		return translate(err)
	})
}

// UpdateVariant changes an existing variant's SKU and/or price (nil keeps a value). It fails with
// ErrNotFound or ErrSKUTaken.
func (s *Store) UpdateVariant(ctx context.Context, params dbgen.UpdateVariantParams) error {
	return db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		_, err := q.UpdateVariant(ctx, params)
		if isUniqueViolation(err) {
			return ErrSKUTaken
		}
		return translate(err)
	})
}

// DeleteVariant removes a product's price for one pot size, unless plants are for sale in it
// (ErrVariantHasStock). It fails with ErrNotFound when there's no such variant.
func (s *Store) DeleteVariant(ctx context.Context, orgID, productID, potSizeID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		variants, err := q.ListVariantsWithStock(ctx, dbgen.ListVariantsWithStockParams{OrganizationID: orgID, ProductID: &productID})
		if err != nil {
			return err
		}
		for _, v := range variants {
			if v.PotSizeID == potSizeID && v.Stock > 0 {
				return ErrVariantHasStock
			}
		}
		deleted, err := q.DeleteVariant(ctx, dbgen.DeleteVariantParams{OrganizationID: orgID, ProductID: productID, PotSizeID: potSizeID})
		if err == nil && deleted == 0 {
			return ErrNotFound
		}
		return err
	})
}

// --- Marking plants for sale ---

// SaleError lists the plants that can't be marked for sale because their pot can't be sold
// (e.g. the Tissue Culture Cup).
type SaleError struct {
	UnsellablePot []string
}

func (e *SaleError) Error() string {
	return "in a pot that can't be sold: " + strings.Join(e.UnsellablePot, ", ")
}

// NewVariant is a SKU created because plants of its kind were marked for sale in that pot for the first time.
type NewVariant struct {
	Product string // the product's title
	PotSize string
	SKU     string
}

// SaleResult counts what happened to the chosen plants.
type SaleResult struct {
	Marked         int
	AlreadyForSale int
	UnsellablePot  int          // skipped (batch mode only)
	NewVariants    []NewVariant // SKUs created along the way; they have no price yet
}

// MarkForSale marks plants for sale: a whole batch (skipping, and counting, those in a pot that
// can't be sold), or the listed barcodes (all of which must be in a sellable pot, or nothing
// changes). Plants of a kind not sold in that pot before get a new product and/or SKU, without a
// price. It fails with ErrNotFound (the batch), a *BarcodeError, or a *SaleError.
func (s *Store) MarkForSale(ctx context.Context, orgID int64, batchID *int64, barcodes []string) (result SaleResult, err error) {
	barcodes = normalizeBarcodes(barcodes)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if batchID != nil {
			if _, err := q.GetBatch(ctx, dbgen.GetBatchParams{OrganizationID: orgID, ID: *batchID}); err != nil {
				return translate(err)
			}
		} else if err := checkBarcodes(ctx, q, orgID, barcodes); err != nil {
			return err
		}

		plants, err := q.SaleEligibility(ctx, dbgen.SaleEligibilityParams{OrganizationID: orgID, BatchID: batchID, Barcodes: barcodes})
		if err != nil {
			return err
		}
		problems := &SaleError{}
		for _, p := range plants {
			switch {
			case p.Status == dbgen.PlantStatusForSale:
				result.AlreadyForSale++
			case p.Status == dbgen.PlantStatusGrowing && !p.PotSellable:
				problems.UnsellablePot = append(problems.UnsellablePot, p.Barcode)
			}
		}
		if batchID == nil && len(problems.UnsellablePot) > 0 {
			return problems
		}
		result.UnsellablePot = len(problems.UnsellablePot)

		// Growing plants about to go on sale, and (to repair anything missed) ones already for sale.
		statuses := []string{string(dbgen.PlantStatusGrowing), string(dbgen.PlantStatusForSale)}
		if result.NewVariants, err = createMissingVariants(ctx, q, orgID, batchID, barcodes, statuses); err != nil {
			return err
		}
		marked, err := q.MarkForSale(ctx, dbgen.MarkForSaleParams{OrganizationID: orgID, BatchID: batchID, Barcodes: barcodes})
		result.Marked = len(marked)
		return err
	})
	return result, err
}

// createMissingVariants gives the chosen plants with one of statuses (in sellable pots) a product
// and a SKU for their pot, where they don't have one yet.
func createMissingVariants(ctx context.Context, q *dbgen.Queries, orgID int64, batchID *int64, barcodes []string, statuses []string) ([]NewVariant, error) {
	missing, err := q.MissingVariants(ctx, dbgen.MissingVariantsParams{OrganizationID: orgID, BatchID: batchID, Barcodes: barcodes, Statuses: statuses})
	if err != nil {
		return nil, fmt.Errorf("find missing variants: %w", err)
	}
	var created []NewVariant
	for _, m := range missing {
		title := m.PlantName
		if m.Variegation != nil {
			title += " " + *m.Variegation
		}
		productID, err := q.EnsureProduct(ctx, dbgen.EnsureProductParams{
			OrganizationID: orgID, PlantNameID: m.PlantNameID, VariegationID: m.VariegationID, Title: title,
		})
		if err != nil {
			return nil, fmt.Errorf("create product: %w", err)
		}
		sku, err := freeSKU(ctx, q, orgID, suggestSKU(m.PlantName, m.Variegation, m.PotSize))
		if err != nil {
			return nil, err
		}
		if _, err := q.CreateVariant(ctx, dbgen.CreateVariantParams{OrganizationID: orgID, ProductID: productID, PotSizeID: m.PotSizeID, Sku: sku}); err != nil {
			return nil, fmt.Errorf("create variant: %w", err)
		}
		created = append(created, NewVariant{Product: title, PotSize: m.PotSize, SKU: sku})
	}
	return created, nil
}

// suggestSKU makes a starting SKU from initials: Alocasia baginda 'Dragon Scale', Aurea, Small Cup
// → "ABDS-AUR-SC". People can change it on the Products page.
func suggestSKU(plantName string, variegation *string, potSize string) string {
	initials := func(text string) string {
		var b strings.Builder
		for _, word := range strings.FieldsFunc(text, func(r rune) bool { return r == ' ' || r == '-' }) {
			word = strings.Trim(word, `'"`)
			if word != "" {
				b.WriteString(strings.ToUpper(word[:1]))
			}
		}
		return b.String()
	}
	parts := []string{initials(plantName)}
	if variegation != nil {
		v := strings.ToUpper(*variegation)
		parts = append(parts, v[:min(3, len(v))])
	}
	return strings.Join(append(parts, initials(potSize)), "-")
}

// freeSKU returns sku, or sku-2, sku-3… if it's already used in the nursery.
func freeSKU(ctx context.Context, q *dbgen.Queries, orgID int64, sku string) (string, error) {
	candidate := sku
	for n := 2; ; n++ {
		taken, err := q.SKUTaken(ctx, dbgen.SKUTakenParams{OrganizationID: orgID, Sku: candidate})
		if err != nil {
			return "", fmt.Errorf("check SKU: %w", err)
		}
		if !taken {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", sku, n)
	}
}

// UnmarkForSale takes plants off sale (back to growing). It returns how many changed, or fails
// with a *BarcodeError.
func (s *Store) UnmarkForSale(ctx context.Context, orgID int64, barcodes []string) (unmarked int, err error) {
	barcodes = normalizeBarcodes(barcodes)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if err := checkBarcodes(ctx, q, orgID, barcodes); err != nil {
			return err
		}
		done, err := q.UnmarkForSale(ctx, dbgen.UnmarkForSaleParams{OrganizationID: orgID, Barcodes: barcodes})
		unmarked = len(done)
		return err
	})
	return unmarked, err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Product images: chosen, in order, from photos of the product's plants ---

const maxProductImages = 20

var ErrPhotoNotEligible = errors.New("images must be photos of this product's plants, each listed once")

// ProductPhotoCandidates lists the photos that can be the product's images (photos of plants with
// its name and variegation), newest first. It fails with ErrNotFound.
func (s *Store) ProductPhotoCandidates(ctx context.Context, orgID, productID int64) (photos []dbgen.ProductPhotoCandidatesRow, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetProduct(ctx, dbgen.GetProductParams{OrganizationID: orgID, ID: productID}); err != nil {
			return translate(err)
		}
		photos, err = q.ProductPhotoCandidates(ctx, dbgen.ProductPhotoCandidatesParams{OrganizationID: orgID, ProductID: productID})
		return err
	})
	return photos, err
}

// SetProductPhotos makes photoIDs the product's images, in that order (the first is the main one).
// It fails with ErrNotFound (the product) or ErrPhotoNotEligible.
func (s *Store) SetProductPhotos(ctx context.Context, orgID, productID int64, photoIDs []int64) error {
	seen := map[int64]bool{}
	for _, id := range photoIDs {
		if seen[id] {
			return ErrPhotoNotEligible
		}
		seen[id] = true
	}
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetProduct(ctx, dbgen.GetProductParams{OrganizationID: orgID, ID: productID}); err != nil {
			return translate(err)
		}
		eligible, err := q.CountEligibleProductPhotos(ctx, dbgen.CountEligibleProductPhotosParams{OrganizationID: orgID, ProductID: productID, PhotoIds: photoIDs})
		if err != nil {
			return err
		}
		if int(eligible) != len(photoIDs) {
			return ErrPhotoNotEligible
		}
		if err := q.ClearProductPhotos(ctx, dbgen.ClearProductPhotosParams{OrganizationID: orgID, ProductID: productID}); err != nil {
			return err
		}
		for position, photoID := range photoIDs {
			err := q.AddProductPhoto(ctx, dbgen.AddProductPhotoParams{OrganizationID: orgID, ProductID: productID, PhotoID: photoID, Position: int32(position)})
			if err != nil {
				return err
			}
		}
		return nil
	})
}
