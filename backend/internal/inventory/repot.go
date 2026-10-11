package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// BarcodeError says which of the listed barcodes can't be used: Unknown ones don't exist in the
// nursery, Finished ones belong to plants that are sold or discarded.
type BarcodeError struct {
	Unknown  []string
	Finished []string
	Held     []string // held for an online order: cancel the order, or hand it over, first
}

func (e *BarcodeError) Error() string {
	var parts []string
	if len(e.Unknown) > 0 {
		parts = append(parts, "no plant with barcode "+strings.Join(e.Unknown, ", "))
	}
	if len(e.Finished) > 0 {
		parts = append(parts, "already sold or discarded: "+strings.Join(e.Finished, ", "))
	}
	if len(e.Held) > 0 {
		parts = append(parts, "held for an online order: "+strings.Join(e.Held, ", "))
	}
	return strings.Join(parts, "; ")
}

// Repot is a move of plants into a new pot size: either a whole batch or the listed barcodes.
type Repot struct {
	PotSizeID int64
	BatchID   *int64   // every growing or for-sale plant in this batch…
	Barcodes  []string // …or these plants
}

// RepotResult counts the chosen plants that moved, and those already in that pot.
type RepotResult struct {
	Moved     int
	Unchanged int
	// SKUs created because for-sale plants moved into a pot they weren't sold in before.
	NewVariants []NewVariant
}

// RepotPlants moves plants into a new pot size (the history trigger records each move).
// It fails with ErrPotSizeNotFound, ErrNotFound (the batch), or a *BarcodeError.
func (s *Store) RepotPlants(ctx context.Context, orgID int64, r Repot) (result RepotResult, err error) {
	barcodes := normalizeBarcodes(r.Barcodes)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		pot, err := q.GetPotSize(ctx, dbgen.GetPotSizeParams{OrganizationID: orgID, ID: r.PotSizeID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrPotSizeNotFound
		} else if err != nil {
			return err
		}

		// How many plants were chosen, to tell "moved" from "already in that pot".
		var chosen int
		if r.BatchID != nil {
			batch, err := q.GetBatch(ctx, dbgen.GetBatchParams{OrganizationID: orgID, ID: *r.BatchID})
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

		moved, err := q.MovePlantsToPot(ctx, dbgen.MovePlantsToPotParams{
			OrganizationID: orgID,
			ToPotSizeID:    pot.ID,
			ToPotSellable:  pot.Sellable,
			BatchID:        r.BatchID,
			Barcodes:       barcodes, // nil when moving a batch: no barcode filter
		})
		if err != nil {
			return fmt.Errorf("move plants: %w", err)
		}
		result = RepotResult{Moved: len(moved), Unchanged: chosen - len(moved)}

		// For-sale plants that moved stay for sale, so their new pot needs a SKU to count in stock.
		result.NewVariants, err = createMissingVariants(ctx, q, orgID, r.BatchID, barcodes, []string{string(dbgen.PlantStatusForSale)})
		return err
	})
	return result, err
}

// DiscardPlants marks plants as discarded, keeping them and their history. It returns how many
// were discarded, or fails with a *BarcodeError.
func (s *Store) DiscardPlants(ctx context.Context, orgID int64, barcodes []string, reason *string) (discarded int, err error) {
	barcodes = normalizeBarcodes(barcodes)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if err := checkBarcodes(ctx, q, orgID, barcodes); err != nil {
			return err
		}
		done, err := q.DiscardPlants(ctx, dbgen.DiscardPlantsParams{OrganizationID: orgID, Barcodes: barcodes, Reason: reason})
		discarded = len(done)
		return err
	})
	return discarded, err
}

// checkBarcodes fails with a *BarcodeError if any barcode is unknown, or its plant is sold, discarded,
// or held for an order.
func checkBarcodes(ctx context.Context, q *dbgen.Queries, orgID int64, barcodes []string) error {
	rows, err := q.GetPlantStatuses(ctx, dbgen.GetPlantStatusesParams{OrganizationID: orgID, Barcodes: barcodes})
	if err != nil {
		return err
	}
	status := map[string]dbgen.PlantStatus{}
	for _, row := range rows {
		status[row.Barcode] = row.Status
	}

	problem := &BarcodeError{}
	for _, barcode := range barcodes {
		switch s, found := status[barcode]; {
		case !found:
			problem.Unknown = append(problem.Unknown, barcode)
		case s == dbgen.PlantStatusSold || s == dbgen.PlantStatusDiscarded:
			problem.Finished = append(problem.Finished, barcode)
		case s == dbgen.PlantStatusHeld:
			problem.Held = append(problem.Held, barcode)
		}
	}
	if len(problem.Unknown) > 0 || len(problem.Finished) > 0 || len(problem.Held) > 0 {
		return problem
	}
	return nil
}

// normalizeBarcodes trims and upper-cases barcodes and drops blanks and repeats (a plant scanned twice).
func normalizeBarcodes(barcodes []string) []string {
	var clean []string
	for _, b := range barcodes {
		b = strings.ToUpper(strings.TrimSpace(b))
		if b != "" && !slices.Contains(clean, b) {
			clean = append(clean, b)
		}
	}
	return clean
}
