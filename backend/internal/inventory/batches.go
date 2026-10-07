package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrPlantNameNotFound   = errors.New("plant name not found")
	ErrVariegationNotFound = errors.New("variegation not found")
	ErrPotSizeNotFound     = errors.New("pot size not found")
)

// BatchSummary is a batch with its plant name, variegation, and how many plants are in each status.
// The generated rows of ListBatches and GetBatch have the same fields, so either converts.
type BatchSummary = dbgen.ListBatchesRow

// PlantDetails is a plant with the names of its batch, plant, variegation, and pot size.
// The generated rows of ListPlants and GetPlantByBarcode have the same fields.
type PlantDetails = dbgen.GetPlantByBarcodeRow

// NewBatch is what's needed to receive a batch of plants.
type NewBatch struct {
	PlantNameID   int64
	VariegationID *int64
	PotSizeID     int64 // the pot every plant starts in, usually the Tissue Culture Cup
	Quantity      int   // how many plants, each getting its own barcode
	ReceivedOn    time.Time
	Source        *string // the lab or seller
	UnitCostCents *int32
	Notes         *string
	CreatedBy     int64
}

// CreateBatch receives a batch: it creates the batch and one plant per Quantity, each with the
// nursery's next barcode, and starts every plant's pot history. It returns the batch's ID.
// It fails with ErrPlantNameNotFound, ErrVariegationNotFound, or ErrPotSizeNotFound.
func (s *Store) CreateBatch(ctx context.Context, orgID int64, nb NewBatch) (batchID int64, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		// Check the choices exist and this nursery may use them (Row-Level Security hides
		// other nurseries' plant names and pot sizes, so they count as not found).
		name, err := q.GetPlantName(ctx, nb.PlantNameID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && name.Rank == dbgen.PlantRankFamily {
			return ErrPlantNameNotFound
		} else if err != nil {
			return err
		}
		if nb.VariegationID != nil {
			if _, err := q.GetVariegation(ctx, *nb.VariegationID); errors.Is(err, pgx.ErrNoRows) {
				return ErrVariegationNotFound
			} else if err != nil {
				return err
			}
		}
		if _, err := q.GetPotSize(ctx, dbgen.GetPotSizeParams{OrganizationID: orgID, ID: nb.PotSizeID}); errors.Is(err, pgx.ErrNoRows) {
			return ErrPotSizeNotFound
		} else if err != nil {
			return err
		}

		org, err := q.GetOrganization(ctx, orgID)
		if err != nil {
			return fmt.Errorf("get organization: %w", err)
		}
		numbers, err := q.ReserveInventoryNumbers(ctx, dbgen.ReserveInventoryNumbersParams{
			OrganizationID: orgID, BarcodePrefix: barcodePrefix(org.Slug), PlantCount: int64(nb.Quantity),
		})
		if err != nil {
			return fmt.Errorf("reserve numbers: %w", err)
		}

		batch, err := q.CreateBatch(ctx, dbgen.CreateBatchParams{
			OrganizationID: orgID,
			Code:           fmt.Sprintf("B-%04d", numbers.BatchNumber),
			PlantNameID:    nb.PlantNameID,
			VariegationID:  nb.VariegationID,
			Source:         nb.Source,
			ReceivedOn:     nb.ReceivedOn,
			UnitCostCents:  nb.UnitCostCents,
			Notes:          nb.Notes,
			CreatedBy:      &nb.CreatedBy,
		})
		if err != nil {
			return fmt.Errorf("create batch: %w", err)
		}
		batchID = batch.ID

		_, err = q.CreateBatchPlants(ctx, dbgen.CreateBatchPlantsParams{
			OrganizationID: orgID,
			BarcodePrefix:  numbers.BarcodePrefix,
			BatchID:        batch.ID,
			PlantNameID:    nb.PlantNameID,
			VariegationID:  nb.VariegationID,
			PotSizeID:      nb.PotSizeID,
			FirstNumber:    numbers.FirstPlantNumber,
			LastNumber:     numbers.FirstPlantNumber + int64(nb.Quantity) - 1,
		})
		if err != nil {
			return fmt.Errorf("create plants: %w", err)
		}
		return q.RecordFirstPots(ctx, dbgen.RecordFirstPotsParams{OrganizationID: orgID, BatchID: batch.ID, ChangedBy: &nb.CreatedBy})
	})
	return batchID, err
}

// barcodePrefix makes a nursery's barcode prefix from its slug: the first letter of each word, up
// to four ("root-access-htx" → "RAH").
func barcodePrefix(slug string) string {
	var prefix strings.Builder
	for _, word := range strings.Split(slug, "-") {
		if word != "" && prefix.Len() < 4 {
			prefix.WriteByte(word[0])
		}
	}
	if prefix.Len() == 0 {
		return "NMS"
	}
	return strings.ToUpper(prefix.String())
}

// Batches lists orgID's batches, most recently received first.
func (s *Store) Batches(ctx context.Context, orgID int64) (batches []BatchSummary, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		batches, err = q.ListBatches(ctx, orgID)
		return err
	})
	return batches, err
}

// Batch returns one batch and its plants, or ErrNotFound.
func (s *Store) Batch(ctx context.Context, orgID, batchID int64) (batch BatchSummary, plants []PlantDetails, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		row, err := q.GetBatch(ctx, dbgen.GetBatchParams{OrganizationID: orgID, ID: batchID})
		if err != nil {
			return translate(err)
		}
		batch = BatchSummary(row)
		plants, err = listPlants(ctx, q, dbgen.ListPlantsParams{OrganizationID: orgID, BatchID: &batchID})
		return err
	})
	return batch, plants, err
}

// Plants lists orgID's plants, filtered by whichever of params' filters are set (up to 1000).
func (s *Store) Plants(ctx context.Context, params dbgen.ListPlantsParams) (plants []PlantDetails, err error) {
	err = db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		plants, err = listPlants(ctx, q, params)
		return err
	})
	return plants, err
}

func listPlants(ctx context.Context, q *dbgen.Queries, params dbgen.ListPlantsParams) ([]PlantDetails, error) {
	rows, err := q.ListPlants(ctx, params)
	if err != nil {
		return nil, err
	}
	plants := make([]PlantDetails, len(rows))
	for i, row := range rows {
		plants[i] = PlantDetails(row)
	}
	return plants, nil
}

// PlantByBarcode returns the plant with barcode and its pot history, or ErrNotFound.
func (s *Store) PlantByBarcode(ctx context.Context, orgID int64, barcode string) (plant PlantDetails, history []dbgen.ListPotChangesRow, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		plant, err = q.GetPlantByBarcode(ctx, dbgen.GetPlantByBarcodeParams{OrganizationID: orgID, Barcode: strings.ToUpper(strings.TrimSpace(barcode))})
		if err != nil {
			return translate(err)
		}
		history, err = q.ListPotChanges(ctx, dbgen.ListPotChangesParams{OrganizationID: orgID, PlantID: plant.ID})
		return err
	})
	return plant, history, err
}

// --- Plant names and variegations: the shared list plus the nursery's own ---

// SearchPlantNames finds up to 50 names (genus or finer) containing term, shortest first.
func (s *Store) SearchPlantNames(ctx context.Context, orgID int64, term string) (names []dbgen.PlantName, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		names, err = q.SearchPlantNames(ctx, strings.TrimSpace(term))
		return err
	})
	return names, err
}

func (s *Store) Variegations(ctx context.Context, orgID int64) (variegations []dbgen.Variegation, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		variegations, err = q.ListVariegations(ctx)
		return err
	})
	return variegations, err
}
