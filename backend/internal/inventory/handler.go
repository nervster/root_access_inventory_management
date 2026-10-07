package inventory

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Routes adds the inventory routes to signedIn, a group behind auth.RequireUser. They live inside
// a nursery (/api/orgs/:orgId/…), so members only, and each checks an inventory permission.
func Routes(signedIn *gin.RouterGroup, orgs *organization.Store, store *Store) {
	h := handler{store: store}
	view := organization.RequirePermission(organization.InventoryView)
	edit := organization.RequirePermission(organization.InventoryEdit)
	sell := organization.RequirePermission(organization.ListingsManage) // products, prices, what's for sale

	org := signedIn.Group("/orgs/:orgId", organization.RequireMember(orgs))
	org.GET("/pot-sizes", view, h.listPotSizes)
	org.POST("/pot-sizes", edit, h.createPotSize)
	org.PUT("/pot-sizes/order", edit, h.reorderPotSizes)
	org.PATCH("/pot-sizes/:potSizeId", edit, h.updatePotSize)
	org.DELETE("/pot-sizes/:potSizeId", edit, h.deletePotSize)

	org.GET("/plant-names", view, h.searchPlantNames)
	org.GET("/variegations", view, h.listVariegations)

	org.GET("/batches", view, h.listBatches)
	org.POST("/batches", edit, h.createBatch)
	org.GET("/batches/:batchId", view, h.getBatch)

	org.GET("/plants", view, h.listPlants)
	org.GET("/plants/by-barcode/:barcode", view, h.getPlantByBarcode)

	org.POST("/repot", edit, h.repot)
	org.POST("/plants/discard", edit, h.discardPlants)

	org.GET("/products", view, h.listProducts)
	org.POST("/products", sell, h.createProduct)
	org.GET("/products/:productId", view, h.getProduct)
	org.PATCH("/products/:productId", sell, h.updateProduct)
	org.PUT("/products/:productId/variants/:potSizeId", sell, h.setVariant)
	org.DELETE("/products/:productId/variants/:potSizeId", sell, h.deleteVariant)
	org.POST("/plants/mark-for-sale", sell, h.markForSale)
	org.POST("/plants/unmark-for-sale", sell, h.unmarkForSale)
}

type handler struct {
	store *Store
}

// --- /api/orgs/:orgId/pot-sizes ---

type potSizeResponse struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	DiameterInches *float64 `json:"diameterInches"`
	HeightInches   *float64 `json:"heightInches"`
	SortOrder      int32    `json:"sortOrder"`
	Sellable       bool     `json:"sellable"`
	Description    *string  `json:"description"`
}

func newPotSizeResponse(p dbgen.PotSize) potSizeResponse {
	return potSizeResponse{
		ID: p.ID, Name: p.Name, DiameterInches: p.DiameterInches, HeightInches: p.HeightInches,
		SortOrder: p.SortOrder, Sellable: p.Sellable, Description: p.Description,
	}
}

func (h handler) listPotSizes(c *gin.Context) {
	sizes, err := h.store.PotSizes(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]potSizeResponse, len(sizes))
	for i, size := range sizes {
		response[i] = newPotSizeResponse(size)
	}
	c.JSON(http.StatusOK, response)
}

// potSizeRequest is the body for creating (name required) and updating (leave out what stays the same).
type potSizeRequest struct {
	Name           *string  `json:"name"`
	DiameterInches *float64 `json:"diameterInches"`
	HeightInches   *float64 `json:"heightInches"`
	Sellable       *bool    `json:"sellable"` // false for Tissue Culture Cup: grown on before selling
	Description    *string  `json:"description"`
}

// validate trims the text fields and checks the values that are present. It returns a message for
// the client, or "" when the request is fine.
func (r *potSizeRequest) validate() string {
	if r.Name != nil {
		name := strings.TrimSpace(*r.Name)
		if name == "" || len(name) > 100 {
			return "name must be 1 to 100 characters"
		}
		r.Name = &name
	}
	for _, inches := range []*float64{r.DiameterInches, r.HeightInches} {
		if inches != nil && (*inches <= 0 || *inches > 1000) {
			return "diameter and height must be more than 0 inches"
		}
	}
	if r.Description != nil {
		description := strings.TrimSpace(*r.Description)
		if len(description) > 2000 {
			return "description must be at most 2000 characters"
		}
		r.Description = &description
	}
	return ""
}

func (h handler) createPotSize(c *gin.Context) {
	var req potSizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if req.Name == nil {
		web.Fail(c, http.StatusBadRequest, "name is required")
		return
	}
	if message := req.validate(); message != "" {
		web.Fail(c, http.StatusBadRequest, message)
		return
	}
	sellable := req.Sellable == nil || *req.Sellable // sellable unless said otherwise

	size, err := h.store.CreatePotSize(c.Request.Context(), dbgen.CreatePotSizeParams{
		OrganizationID: organization.CurrentAccess(c).OrganizationID,
		Name:           *req.Name,
		DiameterInches: req.DiameterInches,
		HeightInches:   req.HeightInches,
		Sellable:       sellable,
		Description:    req.Description,
	})
	if err != nil {
		h.failPotSize(c, err)
		return
	}
	c.JSON(http.StatusCreated, newPotSizeResponse(size))
}

func (h handler) updatePotSize(c *gin.Context) {
	id, ok := web.IDParam(c, "potSizeId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "pot size not found")
		return
	}
	var req potSizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if message := req.validate(); message != "" {
		web.Fail(c, http.StatusBadRequest, message)
		return
	}

	size, err := h.store.UpdatePotSize(c.Request.Context(), dbgen.UpdatePotSizeParams{
		OrganizationID: organization.CurrentAccess(c).OrganizationID,
		ID:             id,
		Name:           req.Name,
		DiameterInches: req.DiameterInches,
		HeightInches:   req.HeightInches,
		Sellable:       req.Sellable,
		Description:    req.Description,
	})
	if err != nil {
		h.failPotSize(c, err)
		return
	}
	c.JSON(http.StatusOK, newPotSizeResponse(size))
}

type reorderPotSizesRequest struct {
	IDs []int64 `json:"ids"` // every pot size, smallest first
}

func (h handler) reorderPotSizes(c *gin.Context) {
	orgID := organization.CurrentAccess(c).OrganizationID
	var req reorderPotSizesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if err := h.store.ReorderPotSizes(c.Request.Context(), orgID, req.IDs); err != nil {
		h.failPotSize(c, err)
		return
	}
	h.listPotSizes(c)
}

func (h handler) deletePotSize(c *gin.Context) {
	id, ok := web.IDParam(c, "potSizeId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "pot size not found")
		return
	}
	if err := h.store.DeletePotSize(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, id); err != nil {
		h.failPotSize(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h handler) failPotSize(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "pot size not found")
	case errors.Is(err, ErrNameTaken):
		web.Fail(c, http.StatusConflict, "there's already a pot size with that name")
	case errors.Is(err, ErrInUse):
		web.Fail(c, http.StatusConflict, "plants or products still use this pot size; move them first")
	case errors.Is(err, ErrWrongPotList):
		web.Fail(c, http.StatusBadRequest, ErrWrongPotList.Error())
	default:
		web.FailInternal(c, err)
	}
}
