package inventory

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

type productResponse struct {
	ID            int64             `json:"id"`
	PlantNameID   int64             `json:"plantNameId"`
	PlantName     string            `json:"plantName"`
	VariegationID *int64            `json:"variegationId"`
	Variegation   *string           `json:"variegation"`
	Title         string            `json:"title"`
	Description   *string           `json:"description"`
	CreatedAt     time.Time         `json:"createdAt"`
	Variants      []variantResponse `json:"variants"` // smallest pot first
}

type variantResponse struct {
	ID         int64  `json:"id"`
	PotSizeID  int64  `json:"potSizeId"`
	PotSize    string `json:"potSize"`
	SKU        string `json:"sku"`
	PriceCents int32  `json:"priceCents"`
	Stock      int64  `json:"stock"` // plants for sale right now
}

func newProductResponse(p ProductWithVariants) productResponse {
	response := productResponse{
		ID: p.ID, PlantNameID: p.PlantNameID, PlantName: p.PlantName, VariegationID: p.VariegationID, Variegation: p.Variegation,
		Title: p.Title, Description: p.Description, CreatedAt: p.CreatedAt, Variants: make([]variantResponse, len(p.Variants)),
	}
	for i, v := range p.Variants {
		response.Variants[i] = variantResponse{ID: v.ID, PotSizeID: v.PotSizeID, PotSize: v.PotSize, SKU: v.Sku, PriceCents: v.PriceCents, Stock: v.Stock}
	}
	return response
}

// --- /products ---

func (h handler) listProducts(c *gin.Context) {
	products, err := h.store.Products(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]productResponse, len(products))
	for i, p := range products {
		response[i] = newProductResponse(p)
	}
	c.JSON(http.StatusOK, response)
}

func (h handler) getProduct(c *gin.Context) {
	productID, ok := web.IDParam(c, "productId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "product not found")
		return
	}
	h.respondWithProduct(c, productID, http.StatusOK)
}

func (h handler) respondWithProduct(c *gin.Context, productID int64, status int) {
	product, err := h.store.Product(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, productID)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "product not found")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.JSON(status, newProductResponse(product))
	}
}

type productRequest struct {
	PlantNameID   int64   `json:"plantNameId"`   // create only
	VariegationID *int64  `json:"variegationId"` // create only
	Title         *string `json:"title"`
	Description   *string `json:"description"`
}

// validate trims the title and description and checks their length; it returns a message for the
// client, or "" when they're fine.
func (r *productRequest) validate() string {
	if r.Title != nil {
		title := strings.TrimSpace(*r.Title)
		if title == "" || len(title) > 200 {
			return "title must be 1 to 200 characters"
		}
		r.Title = &title
	}
	if r.Description != nil {
		description := strings.TrimSpace(*r.Description)
		if len(description) > 5000 {
			return "description must be at most 5000 characters"
		}
		r.Description = &description
	}
	return ""
}

func (h handler) createProduct(c *gin.Context) {
	var req productRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.PlantNameID == 0 {
		web.Fail(c, http.StatusBadRequest, "plantNameId is required")
		return
	}
	if message := req.validate(); message != "" {
		web.Fail(c, http.StatusBadRequest, message)
		return
	}

	productID, err := h.store.CreateProduct(c.Request.Context(), organization.CurrentAccess(c).OrganizationID,
		req.PlantNameID, req.VariegationID, req.Title, req.Description)
	switch {
	case errors.Is(err, ErrPlantNameNotFound):
		web.Fail(c, http.StatusBadRequest, "plant name not found; pick a genus, species, or cultivar")
	case errors.Is(err, ErrVariegationNotFound):
		web.Fail(c, http.StatusBadRequest, "variegation not found")
	case errors.Is(err, ErrProductExists):
		web.Fail(c, http.StatusConflict, ErrProductExists.Error())
	case err != nil:
		web.FailInternal(c, err)
	default:
		h.respondWithProduct(c, productID, http.StatusCreated)
	}
}

func (h handler) updateProduct(c *gin.Context) {
	productID, ok := web.IDParam(c, "productId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "product not found")
		return
	}
	var req productRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if message := req.validate(); message != "" {
		web.Fail(c, http.StatusBadRequest, message)
		return
	}

	err := h.store.UpdateProduct(c.Request.Context(), dbgen.UpdateProductParams{
		OrganizationID: organization.CurrentAccess(c).OrganizationID, ID: productID, Title: req.Title, Description: req.Description,
	})
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "product not found")
	case err != nil:
		web.FailInternal(c, err)
	default:
		h.respondWithProduct(c, productID, http.StatusOK)
	}
}

// --- /products/:productId/variants/:potSizeId ---

type variantRequest struct {
	SKU        string `json:"sku"`
	PriceCents *int32 `json:"priceCents"`
}

// setVariant handles PUT: the SKU and price of a product in one pot size (created if needed).
func (h handler) setVariant(c *gin.Context) {
	productID, ok1 := web.IDParam(c, "productId")
	potSizeID, ok2 := web.IDParam(c, "potSizeId")
	if !ok1 || !ok2 {
		web.Fail(c, http.StatusNotFound, "product or pot size not found")
		return
	}
	var req variantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	sku := strings.TrimSpace(req.SKU)
	switch {
	case sku == "" || len(sku) > 64:
		web.Fail(c, http.StatusBadRequest, "sku must be 1 to 64 characters")
		return
	case req.PriceCents == nil || *req.PriceCents < 0:
		web.Fail(c, http.StatusBadRequest, "priceCents is required and can't be negative")
		return
	}

	err := h.store.SetVariant(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, productID, potSizeID, sku, *req.PriceCents)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "product not found")
	case errors.Is(err, ErrPotSizeNotFound):
		web.Fail(c, http.StatusNotFound, "pot size not found")
	case errors.Is(err, ErrPotNotSellable):
		web.Fail(c, http.StatusBadRequest, ErrPotNotSellable.Error())
	case errors.Is(err, ErrSKUTaken):
		web.Fail(c, http.StatusConflict, ErrSKUTaken.Error())
	case err != nil:
		web.FailInternal(c, err)
	default:
		h.respondWithProduct(c, productID, http.StatusOK)
	}
}

func (h handler) deleteVariant(c *gin.Context) {
	productID, ok1 := web.IDParam(c, "productId")
	potSizeID, ok2 := web.IDParam(c, "potSizeId")
	if !ok1 || !ok2 {
		web.Fail(c, http.StatusNotFound, "variant not found")
		return
	}
	err := h.store.DeleteVariant(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, productID, potSizeID)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "variant not found")
	case errors.Is(err, ErrVariantHasStock):
		web.Fail(c, http.StatusConflict, "plants are for sale in that pot size; take them off sale first")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

// --- /plants/mark-for-sale and /plants/unmark-for-sale ---

type saleRequest struct {
	BatchID  *int64   `json:"batchId"`  // a whole batch (only for mark-for-sale)…
	Barcodes []string `json:"barcodes"` // …or these plants
}

type saleResponse struct {
	Marked         int `json:"marked"`
	AlreadyForSale int `json:"alreadyForSale"`
	Skipped        struct {
		UnsellablePot int `json:"unsellablePot"` // still in e.g. the Tissue Culture Cup
		NoPrice       int `json:"noPrice"`       // no variant for their plant and pot size yet
	} `json:"skipped"`
}

func (h handler) markForSale(c *gin.Context) {
	var req saleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	switch {
	case (req.BatchID == nil) == (len(req.Barcodes) == 0):
		web.Fail(c, http.StatusBadRequest, "give either batchId or barcodes")
		return
	case len(req.Barcodes) > maxBarcodes:
		web.Fail(c, http.StatusBadRequest, "at most 1000 barcodes at a time")
		return
	}

	result, err := h.store.MarkForSale(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, req.BatchID, req.Barcodes)
	var barcodeErr *BarcodeError
	var saleErr *SaleError
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "batch not found")
		return
	case errors.As(err, &barcodeErr):
		web.Fail(c, http.StatusBadRequest, barcodeErr.Error())
		return
	case errors.As(err, &saleErr):
		web.Fail(c, http.StatusBadRequest, "can't mark these for sale: "+saleErr.Error())
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	response := saleResponse{Marked: result.Marked, AlreadyForSale: result.AlreadyForSale}
	response.Skipped.UnsellablePot, response.Skipped.NoPrice = result.UnsellablePot, result.NoPrice
	c.JSON(http.StatusOK, response)
}

func (h handler) unmarkForSale(c *gin.Context) {
	var req saleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.BatchID != nil || len(req.Barcodes) == 0 || len(req.Barcodes) > maxBarcodes {
		web.Fail(c, http.StatusBadRequest, "give 1 to 1000 barcodes")
		return
	}
	unmarked, err := h.store.UnmarkForSale(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, req.Barcodes)
	var barcodeErr *BarcodeError
	switch {
	case errors.As(err, &barcodeErr):
		web.Fail(c, http.StatusBadRequest, barcodeErr.Error())
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.JSON(http.StatusOK, gin.H{"unmarked": unmarked})
	}
}
