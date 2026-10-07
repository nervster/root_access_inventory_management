package inventory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestParallelBatchesGetDistinctBarcodes receives batches at the same moment, through the real
// connection pool (not one test transaction), and checks no barcode or batch code is handed out twice.
func TestParallelBatchesGetDistinctBarcodes(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	q := dbgen.New(pool)

	// This test commits its data, so it removes it afterwards.
	org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: "Parallel", Slug: "parallel-batches-test", TimeZone: "America/Chicago"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_parallel_batches", Email: "parallel@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if _, err := pool.Exec(ctx, "DELETE FROM organizations WHERE id = $1", org.ID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	var cupID, nameID int64
	if err := pool.QueryRow(ctx, "INSERT INTO pot_sizes (organization_id, name) VALUES ($1, 'Cup') RETURNING id", org.ID).Scan(&cupID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT id FROM plant_names WHERE name = 'Dragon Scale' AND organization_id IS NULL").Scan(&nameID); err != nil {
		t.Fatal(err)
	}

	store := NewStore(pool)
	const batches, perBatch = 10, 5
	var wg sync.WaitGroup
	errs := make(chan error, batches)
	for range batches {
		wg.Go(func() {
			_, err := store.CreateBatch(ctx, org.ID, NewBatch{
				PlantNameID: nameID, PotSizeID: cupID, Quantity: perBatch, ReceivedOn: time.Now(), CreatedBy: user.ID,
			})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	var plants, barcodes, codes int
	err = pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT barcode), (SELECT count(DISTINCT code) FROM batches WHERE organization_id = $1)
		FROM plants WHERE organization_id = $1`, org.ID).Scan(&plants, &barcodes, &codes)
	if err != nil {
		t.Fatal(err)
	}
	if plants != batches*perBatch || barcodes != plants || codes != batches {
		t.Errorf("%d plants with %d distinct barcodes in %d distinct batch codes; want %d, %d, %d",
			plants, barcodes, codes, batches*perBatch, batches*perBatch, batches)
	}
}
