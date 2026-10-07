package inventory

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

const maxBarcodes = 1000

type repotRequest struct {
	PotSizeID int64    `json:"potSizeId"`
	BatchID   *int64   `json:"batchId"`  // a whole batch…
	Barcodes  []string `json:"barcodes"` // …or these plants
}

type repotResponse struct {
	Moved       int              `json:"moved"`
	Unchanged   int              `json:"unchanged"`   // already in that pot
	NewVariants []newVariantJSON `json:"newVariants"` // SKUs created for for-sale plants in a new pot
}

// repot handles POST /repot: moves a whole batch, or the listed plants, into a new pot size.
func (h handler) repot(c *gin.Context) {
	access := organization.CurrentAccess(c)
	var req repotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	switch {
	case req.PotSizeID == 0:
		web.Fail(c, http.StatusBadRequest, "potSizeId is required")
		return
	case (req.BatchID == nil) == (len(req.Barcodes) == 0):
		web.Fail(c, http.StatusBadRequest, "give either batchId or barcodes")
		return
	case len(req.Barcodes) > maxBarcodes:
		web.Fail(c, http.StatusBadRequest, "at most 1000 barcodes at a time")
		return
	}

	result, err := h.store.RepotPlants(c.Request.Context(), access.OrganizationID, Repot{
		PotSizeID: req.PotSizeID, BatchID: req.BatchID, Barcodes: req.Barcodes, ChangedBy: auth.CurrentUser(c).ID,
	})
	var barcodeErr *BarcodeError
	switch {
	case errors.Is(err, ErrPotSizeNotFound):
		web.Fail(c, http.StatusBadRequest, "pot size not found")
		return
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "batch not found")
		return
	case errors.As(err, &barcodeErr):
		web.Fail(c, http.StatusBadRequest, barcodeErr.Error())
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	response := repotResponse{Moved: result.Moved, Unchanged: result.Unchanged, NewVariants: make([]newVariantJSON, len(result.NewVariants))}
	for i, v := range result.NewVariants {
		response.NewVariants[i] = newVariantJSON(v)
	}
	c.JSON(http.StatusOK, response)
}

type discardRequest struct {
	Barcodes []string `json:"barcodes"`
	Reason   *string  `json:"reason"` // e.g. "root rot"
}

// discardPlants handles POST /plants/discard: plants that didn't make it. They and their history stay.
func (h handler) discardPlants(c *gin.Context) {
	var req discardRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	if len(req.Barcodes) == 0 || len(req.Barcodes) > maxBarcodes {
		web.Fail(c, http.StatusBadRequest, "give 1 to 1000 barcodes")
		return
	}

	discarded, err := h.store.DiscardPlants(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, req.Barcodes, trimmed(req.Reason))
	var barcodeErr *BarcodeError
	switch {
	case errors.As(err, &barcodeErr):
		web.Fail(c, http.StatusBadRequest, barcodeErr.Error())
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"discarded": discarded})
}
