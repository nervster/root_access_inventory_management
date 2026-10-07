package inventory

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrProductExists   = errors.New("there's already a product for that plant and variegation")
	ErrSKUTaken        = errors.New("that SKU is already used")
	ErrPotNotSellable  = errors.New("plants in that pot size can't be sold")
	ErrVariantHasStock = errors.New("plants are for sale in that pot size")
)

// ProductWithVariants is a product with its variants (one per priced pot size), smallest pot
// first, each with its live stock.
type ProductWithVariants struct {
	dbgen.ListProductsRow
	Variants []dbgen.ListVariantsWithStockRow
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
		byProduct := map[int64][]dbgen.ListVariantsWithStockRow{}
		for _, v := range variants {
			byProduct[v.ProductID] = append(byProduct[v.ProductID], v)
		}
		products = make([]ProductWithVariants, len(rows))
		for i, row := range rows {
			products[i] = ProductWithVariants{ListProductsRow: row, Variants: byProduct[row.ID]}
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
		product = ProductWithVariants{ListProductsRow: dbgen.ListProductsRow(row), Variants: variants}
		return err
	})
	return product, err
}

// CreateProduct creates the product for a plant name and variegation. With no title, it's named
// after the plant ("Alocasia baginda 'Dragon Scale' Aurea"). It fails with ErrPlantNameNotFound,
// ErrVariegationNotFound, or ErrProductExists.
func (s *Store) CreateProduct(ctx context.Context, orgID, plantNameID int64, variegationID *int64, title, description *string) (productID int64, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		name, err := q.GetPlantName(ctx, plantNameID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && name.Rank == dbgen.PlantRankFamily {
			return ErrPlantNameNotFound
		} else if err != nil {
			return err
		}
		defaultTitle := name.FullName
		if variegationID != nil {
			variegation, err := q.GetVariegation(ctx, *variegationID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrVariegationNotFound
			} else if err != nil {
				return err
			}
			defaultTitle += " " + variegation.Name
		}
		if title == nil {
			title = &defaultTitle
		}

		product, err := q.CreateProduct(ctx, dbgen.CreateProductParams{
			OrganizationID: orgID, PlantNameID: plantNameID, VariegationID: variegationID, Title: *title, Description: description,
		})
		if isUniqueViolation(err) {
			return ErrProductExists
		}
		productID = product.ID
		return err
	})
	return productID, err
}

// UpdateProduct changes the title or description (nil keeps a value). It fails with ErrNotFound.
func (s *Store) UpdateProduct(ctx context.Context, params dbgen.UpdateProductParams) error {
	return db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		_, err := q.UpdateProduct(ctx, params)
		return translate(err)
	})
}

// SetVariant sets the SKU and price of a product in one pot size, creating the variant if needed.
// It fails with ErrNotFound (the product), ErrPotSizeNotFound, ErrPotNotSellable, or ErrSKUTaken.
func (s *Store) SetVariant(ctx context.Context, orgID, productID, potSizeID int64, sku string, priceCents int32) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if _, err := q.GetProduct(ctx, dbgen.GetProductParams{OrganizationID: orgID, ID: productID}); err != nil {
			return translate(err)
		}
		pot, err := q.GetPotSize(ctx, dbgen.GetPotSizeParams{OrganizationID: orgID, ID: potSizeID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPotSizeNotFound
		} else if err != nil {
			return err
		}
		if !pot.Sellable {
			return ErrPotNotSellable
		}

		_, err = q.UpsertVariant(ctx, dbgen.UpsertVariantParams{
			OrganizationID: orgID, ProductID: productID, PotSizeID: potSizeID, Sku: sku, PriceCents: priceCents,
		})
		if isUniqueViolation(err) { // the SKU belongs to another variant
			return ErrSKUTaken
		}
		return err
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

// SaleError lists the plants that can't be marked for sale: in a pot that can't be sold, or
// without a price for their plant and pot size.
type SaleError struct {
	UnsellablePot []string
	NoPrice       []string
}

func (e *SaleError) Error() string {
	var parts []string
	if len(e.UnsellablePot) > 0 {
		parts = append(parts, "in a pot that can't be sold: "+strings.Join(e.UnsellablePot, ", "))
	}
	if len(e.NoPrice) > 0 {
		parts = append(parts, "no price for their plant and pot size yet: "+strings.Join(e.NoPrice, ", "))
	}
	return strings.Join(parts, "; ")
}

// SaleResult counts what happened to the chosen plants.
type SaleResult struct {
	Marked         int
	AlreadyForSale int
	UnsellablePot  int // skipped (batch mode only)
	NoPrice        int // skipped (batch mode only)
}

// MarkForSale marks plants for sale: a whole batch (skipping, and counting, those that can't be
// sold yet), or the listed barcodes (all of which must be sellable, or nothing changes). It fails
// with ErrNotFound (the batch), a *BarcodeError, or a *SaleError.
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
			case p.Status != dbgen.PlantStatusGrowing: // sold or discarded: not part of a batch's stock
			case !p.PotSellable:
				problems.UnsellablePot = append(problems.UnsellablePot, p.Barcode)
			case !p.HasPrice:
				problems.NoPrice = append(problems.NoPrice, p.Barcode)
			}
		}
		if batchID == nil && (len(problems.UnsellablePot) > 0 || len(problems.NoPrice) > 0) {
			return problems
		}
		result.UnsellablePot, result.NoPrice = len(problems.UnsellablePot), len(problems.NoPrice)

		marked, err := q.MarkForSale(ctx, dbgen.MarkForSaleParams{OrganizationID: orgID, BatchID: batchID, Barcodes: barcodes})
		result.Marked = len(marked)
		return err
	})
	return result, err
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
