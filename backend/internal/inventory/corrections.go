package inventory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Correcting what plants are (a tissue-culture plant can lose its variegation; a lab can mislabel
// a batch), plants' notes, and batches' details.

// Correction changes the name and/or variegation of a whole batch's living plants, or the listed ones.
type Correction struct {
	BatchID        *int64
	Barcodes       []string
	PlantNameID    *int64 // nil keeps the name
	SetVariegation bool   // false keeps the variegation…
	VariegationID  *int64 // …otherwise this one, nil for none
}

// CorrectionResult counts the chosen plants that changed and those already right.
type CorrectionResult struct {
	Corrected   int
	Unchanged   int
	NewVariants []NewVariant // SKUs created for for-sale plants that are now a different product
}

// CorrectPlants fixes plants' name and/or variegation; correcting a whole batch fixes the batch's
// own record too. History records each correction. For-sale plants move to the right product, which
// gets a SKU for their pot if it lacks one. It fails with ErrPlantNameNotFound,
// ErrVariegationNotFound, ErrNotFound (the batch), or a *BarcodeError.
func (s *Store) CorrectPlants(ctx context.Context, orgID int64, c Correction) (result CorrectionResult, err error) {
	barcodes := normalizeBarcodes(c.Barcodes)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if c.PlantNameID != nil {
			name, err := q.GetPlantName(ctx, *c.PlantNameID)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && name.Rank == dbgen.PlantRankFamily {
				return ErrPlantNameNotFound
			} else if err != nil {
				return err
			}
		}
		if c.SetVariegation && c.VariegationID != nil {
			if _, err := q.GetVariegation(ctx, *c.VariegationID); errors.Is(err, pgx.ErrNoRows) {
				return ErrVariegationNotFound
			} else if err != nil {
				return err
			}
		}

		var chosen int
		if c.BatchID != nil {
			batch, err := q.GetBatch(ctx, dbgen.GetBatchParams{OrganizationID: orgID, ID: *c.BatchID})
			if err != nil {
				return translate(err)
			}
			chosen = int(batch.GrowingCount + batch.ForSaleCount)
		} else {
			if err := checkBarcodes(ctx, q, orgID, barcodes); err != nil {
				return err
			}
			chosen = len(barcodes)
		}

		corrected, err := q.CorrectPlants(ctx, dbgen.CorrectPlantsParams{
			OrganizationID: orgID, BatchID: c.BatchID, Barcodes: barcodes,
			PlantNameID: c.PlantNameID, SetVariegation: c.SetVariegation, VariegationID: c.VariegationID,
		})
		if err != nil {
			return fmt.Errorf("correct plants: %w", err)
		}
		result.Corrected, result.Unchanged = len(corrected), chosen-len(corrected)

		if c.BatchID != nil {
			err := q.SetBatchPlantName(ctx, dbgen.SetBatchPlantNameParams{
				OrganizationID: orgID, ID: *c.BatchID, PlantNameID: c.PlantNameID, SetVariegation: c.SetVariegation, VariegationID: c.VariegationID,
			})
			if err != nil {
				return fmt.Errorf("correct batch: %w", err)
			}
		}
		result.NewVariants, err = createMissingVariants(ctx, q, orgID, c.BatchID, barcodes, []string{string(dbgen.PlantStatusForSale)})
		return err
	})
	return result, err
}

// SetPlantNotes replaces a plant's notes ("" clears them). It fails with ErrNotFound.
func (s *Store) SetPlantNotes(ctx context.Context, orgID int64, barcode, notes string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.UpdatePlantNotes(ctx, dbgen.UpdatePlantNotesParams{
			OrganizationID: orgID, Barcode: strings.ToUpper(strings.TrimSpace(barcode)), Notes: notes,
		})
		return translate(err)
	})
}

// UpdateBatch fixes a batch's details. It fails with ErrNotFound.
func (s *Store) UpdateBatch(ctx context.Context, params dbgen.UpdateBatchParams) error {
	return db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		_, err := q.UpdateBatch(ctx, params)
		return translate(err)
	})
}

// --- Handlers ---

type correctRequest struct {
	BatchID       *int64              `json:"batchId"`       // a whole batch…
	Barcodes      []string            `json:"barcodes"`      // …or these plants
	PlantNameID   *int64              `json:"plantNameId"`   // leave out to keep the name
	VariegationID web.Optional[int64] `json:"variegationId"` // leave out to keep; null for none
}

type correctResponse struct {
	Corrected   int              `json:"corrected"`
	Unchanged   int              `json:"unchanged"` // already right
	NewVariants []newVariantJSON `json:"newVariants"`
}

// correctPlants handles POST /plants/correct.
func (h handler) correctPlants(c *gin.Context) {
	var req correctRequest
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
	case req.PlantNameID == nil && !req.VariegationID.Set:
		web.Fail(c, http.StatusBadRequest, "give plantNameId and/or variegationId to change")
		return
	}

	result, err := h.store.CorrectPlants(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, Correction{
		BatchID: req.BatchID, Barcodes: req.Barcodes, PlantNameID: req.PlantNameID,
		SetVariegation: req.VariegationID.Set, VariegationID: req.VariegationID.Value,
	})
	var barcodeErr *BarcodeError
	switch {
	case errors.Is(err, ErrPlantNameNotFound):
		web.Fail(c, http.StatusBadRequest, "plant name not found; pick a genus, species, or cultivar")
		return
	case errors.Is(err, ErrVariegationNotFound):
		web.Fail(c, http.StatusBadRequest, "variegation not found")
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
	response := correctResponse{Corrected: result.Corrected, Unchanged: result.Unchanged, NewVariants: make([]newVariantJSON, len(result.NewVariants))}
	for i, v := range result.NewVariants {
		response.NewVariants[i] = newVariantJSON(v)
	}
	c.JSON(http.StatusOK, response)
}

// updatePlant handles PATCH /plants/by-barcode/:barcode {notes}.
func (h handler) updatePlant(c *gin.Context) {
	var req struct {
		Notes *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Notes == nil {
		web.Fail(c, http.StatusBadRequest, `send {"notes": "…"} ("" clears them)`)
		return
	}
	notes := strings.TrimSpace(*req.Notes)
	if len(notes) > 2000 {
		web.Fail(c, http.StatusBadRequest, "notes must be at most 2000 characters")
		return
	}
	err := h.store.SetPlantNotes(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, c.Param("barcode"), notes)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "no plant with that barcode")
	case err != nil:
		web.FailInternal(c, err)
	default:
		h.getPlantByBarcode(c)
	}
}

type updateBatchRequest struct {
	Source        *string             `json:"source"`        // "" clears it
	Notes         *string             `json:"notes"`         // "" clears them
	ReceivedOn    *string             `json:"receivedOn"`    // 2026-10-06
	UnitCostCents web.Optional[int32] `json:"unitCostCents"` // needs financials.view; null for unknown
}

// updateBatch handles PATCH /batches/:batchId: fixing a batch's details.
func (h handler) updateBatch(c *gin.Context) {
	access := organization.CurrentAccess(c)
	batchID, ok := web.IDParam(c, "batchId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "batch not found")
		return
	}
	var req updateBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	params := dbgen.UpdateBatchParams{OrganizationID: access.OrganizationID, ID: batchID, Source: trimmedKeepEmpty(req.Source), Notes: trimmedKeepEmpty(req.Notes)}
	if req.ReceivedOn != nil {
		day, err := time.Parse(time.DateOnly, *req.ReceivedOn)
		if err != nil {
			web.Fail(c, http.StatusBadRequest, "receivedOn must be a date like 2026-10-06")
			return
		}
		params.ReceivedOn = &day
	}
	if req.UnitCostCents.Set {
		if !organization.Has(access.Role, organization.FinancialsView) {
			web.Fail(c, http.StatusForbidden, "only roles that see financials can set a cost")
			return
		}
		if req.UnitCostCents.Value != nil && *req.UnitCostCents.Value < 0 {
			web.Fail(c, http.StatusBadRequest, "unitCostCents can't be negative")
			return
		}
		params.SetCost, params.UnitCostCents = true, req.UnitCostCents.Value
	}
	if (params.Source != nil && len(*params.Source) > 200) || (params.Notes != nil && len(*params.Notes) > 2000) {
		web.Fail(c, http.StatusBadRequest, "source must be at most 200 characters and notes at most 2000")
		return
	}

	switch err := h.store.UpdateBatch(c.Request.Context(), params); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "batch not found")
	case err != nil:
		web.FailInternal(c, err)
	default:
		h.respondWithBatch(c, batchID, http.StatusOK)
	}
}

// trimmedKeepEmpty trims text but keeps "" (meaning "clear it"), unlike trimmed.
func trimmedKeepEmpty(text *string) *string {
	if text == nil {
		return nil
	}
	t := strings.TrimSpace(*text)
	return &t
}
