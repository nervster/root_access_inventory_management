package inventory

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

const maxBatchSize = 500

// --- GET /plant-names?search=… and GET /variegations: choices when receiving a batch ---

type plantNameResponse struct {
	ID       int64           `json:"id"`
	Rank     dbgen.PlantRank `json:"rank"`
	FullName string          `json:"fullName"` // Alocasia baginda 'Dragon Scale'
	Notes    *string         `json:"notes"`    // e.g. the trade name
	Shared   bool            `json:"shared"`   // from Plant Stock's shared list, not this nursery's own
}

func (h handler) searchPlantNames(c *gin.Context) {
	names, err := h.store.SearchPlantNames(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, c.Query("search"), c.Query("includeFamilies") == "true")
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]plantNameResponse, len(names))
	for i, n := range names {
		response[i] = plantNameResponse{ID: n.ID, Rank: n.Rank, FullName: n.FullName, Notes: n.Notes, Shared: n.OrganizationID == nil}
	}
	c.JSON(http.StatusOK, response)
}

type variegationResponse struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	Notes  *string `json:"notes"`
	Shared bool    `json:"shared"`
}

func (h handler) listVariegations(c *gin.Context) {
	variegations, err := h.store.Variegations(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]variegationResponse, len(variegations))
	for i, v := range variegations {
		response[i] = variegationResponse{ID: v.ID, Name: v.Name, Notes: v.Notes, Shared: v.OrganizationID == nil}
	}
	c.JSON(http.StatusOK, response)
}

// --- /batches ---

type batchResponse struct {
	ID            int64           `json:"id"`
	Code          string          `json:"code"` // B-0007
	PlantNameID   int64           `json:"plantNameId"`
	PlantName     string          `json:"plantName"`
	VariegationID *int64          `json:"variegationId"`
	Variegation   *string         `json:"variegation"`
	Source        *string         `json:"source"`
	ReceivedOn    string          `json:"receivedOn"`              // 2026-10-06
	UnitCostCents *int32          `json:"unitCostCents,omitempty"` // only for roles with financials.view
	Notes         *string         `json:"notes"`
	CreatedAt     time.Time       `json:"createdAt"`
	PlantCounts   plantCountsJSON `json:"plantCounts"`
}

type plantCountsJSON struct {
	Growing   int64 `json:"growing"`
	ForSale   int64 `json:"forSale"`
	Sold      int64 `json:"sold"`
	Discarded int64 `json:"discarded"`
}

// newBatchResponse leaves out the cost unless showCost (the member may see financials).
func newBatchResponse(b BatchSummary, showCost bool) batchResponse {
	response := batchResponse{
		ID: b.ID, Code: b.Code, PlantNameID: b.PlantNameID, PlantName: b.PlantName,
		VariegationID: b.VariegationID, Variegation: b.Variegation, Source: b.Source,
		ReceivedOn: b.ReceivedOn.Format(time.DateOnly), Notes: b.Notes, CreatedAt: b.CreatedAt,
		PlantCounts: plantCountsJSON{Growing: b.GrowingCount, ForSale: b.ForSaleCount, Sold: b.SoldCount, Discarded: b.DiscardedCount},
	}
	if showCost {
		response.UnitCostCents = b.UnitCostCents
	}
	return response
}

type plantResponse struct {
	ID            int64             `json:"id"`
	Barcode       string            `json:"barcode"` // RAH-000123
	BatchID       int64             `json:"batchId"`
	BatchCode     string            `json:"batchCode"`
	PlantNameID   int64             `json:"plantNameId"`
	PlantName     string            `json:"plantName"`
	VariegationID *int64            `json:"variegationId"`
	Variegation   *string           `json:"variegation"`
	PotSizeID     int64             `json:"potSizeId"`
	PotSize       string            `json:"potSize"`
	Status        dbgen.PlantStatus `json:"status"`
	Notes         *string           `json:"notes"`
	CreatedAt     time.Time         `json:"createdAt"`
	DiscardedAt   *time.Time        `json:"discardedAt"`
	DiscardReason *string           `json:"discardReason"`
}

func newPlantResponse(p PlantDetails) plantResponse {
	return plantResponse{
		ID: p.ID, Barcode: p.Barcode, BatchID: p.BatchID, BatchCode: p.BatchCode,
		PlantNameID: p.PlantNameID, PlantName: p.PlantName, VariegationID: p.VariegationID, Variegation: p.Variegation,
		PotSizeID: p.PotSizeID, PotSize: p.PotSize, Status: p.Status, Notes: p.Notes, CreatedAt: p.CreatedAt,
		DiscardedAt: p.DiscardedAt, DiscardReason: p.DiscardReason,
	}
}

func newPlantResponses(plants []PlantDetails) []plantResponse {
	response := make([]plantResponse, len(plants))
	for i, p := range plants {
		response[i] = newPlantResponse(p)
	}
	return response
}

type batchWithPlantsResponse struct {
	Batch  batchResponse   `json:"batch"`
	Plants []plantResponse `json:"plants"`
}

type createBatchRequest struct {
	PlantNameID   int64   `json:"plantNameId"`
	VariegationID *int64  `json:"variegationId"`
	PotSizeID     int64   `json:"potSizeId"` // where every plant starts, usually the Tissue Culture Cup
	Quantity      int     `json:"quantity"`
	ReceivedOn    string  `json:"receivedOn"` // 2026-10-06
	Source        *string `json:"source"`
	UnitCostCents *int32  `json:"unitCostCents"` // needs financials.view
	Notes         *string `json:"notes"`
}

// createBatch receives a batch of plants, each with its own new barcode.
func (h handler) createBatch(c *gin.Context) {
	access := organization.CurrentAccess(c)
	var req createBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	receivedOn, err := time.Parse(time.DateOnly, req.ReceivedOn)
	switch {
	case req.PlantNameID == 0 || req.PotSizeID == 0:
		web.Fail(c, http.StatusBadRequest, "plantNameId and potSizeId are required")
		return
	case req.Quantity < 1 || req.Quantity > maxBatchSize:
		web.Fail(c, http.StatusBadRequest, "quantity must be 1 to 500")
		return
	case err != nil:
		web.Fail(c, http.StatusBadRequest, "receivedOn must be a date like 2026-10-06")
		return
	case req.UnitCostCents != nil && *req.UnitCostCents < 0:
		web.Fail(c, http.StatusBadRequest, "unitCostCents can't be negative")
		return
	case req.UnitCostCents != nil && !organization.Has(access.Role, organization.FinancialsView):
		web.Fail(c, http.StatusForbidden, "only roles that see financials can set a cost")
		return
	}

	batchID, err := h.store.CreateBatch(c.Request.Context(), access.OrganizationID, NewBatch{
		PlantNameID:   req.PlantNameID,
		VariegationID: req.VariegationID,
		PotSizeID:     req.PotSizeID,
		Quantity:      req.Quantity,
		ReceivedOn:    receivedOn,
		Source:        trimmed(req.Source),
		UnitCostCents: req.UnitCostCents,
		Notes:         trimmed(req.Notes),
		CreatedBy:     auth.CurrentUser(c).ID,
	})
	switch {
	case errors.Is(err, ErrPlantNameNotFound):
		web.Fail(c, http.StatusBadRequest, "plant name not found; pick a genus, species, or cultivar")
		return
	case errors.Is(err, ErrVariegationNotFound):
		web.Fail(c, http.StatusBadRequest, "variegation not found")
		return
	case errors.Is(err, ErrPotSizeNotFound):
		web.Fail(c, http.StatusBadRequest, "pot size not found")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	h.respondWithBatch(c, batchID, http.StatusCreated)
}

func (h handler) listBatches(c *gin.Context) {
	access := organization.CurrentAccess(c)
	batches, err := h.store.Batches(c.Request.Context(), access.OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	showCost := organization.Has(access.Role, organization.FinancialsView)
	response := make([]batchResponse, len(batches))
	for i, b := range batches {
		response[i] = newBatchResponse(b, showCost)
	}
	c.JSON(http.StatusOK, response)
}

func (h handler) getBatch(c *gin.Context) {
	batchID, ok := web.IDParam(c, "batchId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "batch not found")
		return
	}
	h.respondWithBatch(c, batchID, http.StatusOK)
}

func (h handler) respondWithBatch(c *gin.Context, batchID int64, status int) {
	access := organization.CurrentAccess(c)
	batch, plants, err := h.store.Batch(c.Request.Context(), access.OrganizationID, batchID)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "batch not found")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	c.JSON(status, batchWithPlantsResponse{
		Batch:  newBatchResponse(batch, organization.Has(access.Role, organization.FinancialsView)),
		Plants: newPlantResponses(plants),
	})
}

// --- /plants ---

// listPlants handles GET /plants?batchId=…&status=…&potSizeId=… (all filters optional).
func (h handler) listPlants(c *gin.Context) {
	params := dbgen.ListPlantsParams{OrganizationID: organization.CurrentAccess(c).OrganizationID}
	var ok bool
	if params.BatchID, ok = optionalID(c, "batchId"); !ok {
		web.Fail(c, http.StatusBadRequest, "batchId must be a number")
		return
	}
	if params.PotSizeID, ok = optionalID(c, "potSizeId"); !ok {
		web.Fail(c, http.StatusBadRequest, "potSizeId must be a number")
		return
	}
	if status := c.Query("status"); status != "" {
		s := dbgen.PlantStatus(status)
		if s != dbgen.PlantStatusGrowing && s != dbgen.PlantStatusForSale && s != dbgen.PlantStatusSold && s != dbgen.PlantStatusDiscarded {
			web.Fail(c, http.StatusBadRequest, "status must be growing, for_sale, sold, or discarded")
			return
		}
		params.Status = &s
	}

	plants, err := h.store.Plants(c.Request.Context(), params)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, newPlantResponses(plants))
}

// eventResponse is one entry in a plant's history.
type eventResponse struct {
	Type       dbgen.PlantEventType `json:"type"`       // received, repotted, marked_for_sale, …
	Details    json.RawMessage      `json:"details"`    // depends on type, e.g. {"fromPotSize": …, "toPotSize": …}
	OccurredAt time.Time            `json:"occurredAt"` //
	ChangedBy  *string              `json:"changedBy"`  // who: their name (email if they have none); null for system changes
}

type plantWithHistoryResponse struct {
	plantResponse
	History []eventResponse `json:"history"` // oldest first
}

// getPlantByBarcode handles GET /plants/by-barcode/:barcode, e.g. after scanning a label.
func (h handler) getPlantByBarcode(c *gin.Context) {
	plant, history, err := h.store.PlantByBarcode(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, c.Param("barcode"))
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "no plant with that barcode")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	response := plantWithHistoryResponse{plantResponse: newPlantResponse(plant), History: make([]eventResponse, len(history))}
	for i, event := range history {
		response.History[i] = eventResponse{Type: event.Type, Details: event.Details, OccurredAt: event.OccurredAt, ChangedBy: event.ChangedByName}
	}
	c.JSON(http.StatusOK, response)
}

// --- helpers ---

// optionalID reads an optional numeric query parameter: nil when absent, ok false when invalid.
func optionalID(c *gin.Context, name string) (id *int64, ok bool) {
	text := c.Query(name)
	if text == "" {
		return nil, true
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value <= 0 {
		return nil, false
	}
	return &value, true
}

// trimmed trims text; empty text becomes nil (not given).
func trimmed(text *string) *string {
	if text == nil {
		return nil
	}
	t := strings.TrimSpace(*text)
	if t == "" {
		return nil
	}
	return &t
}
