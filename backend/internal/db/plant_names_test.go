package db_test

import (
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestPlantNamesSharedAndOwn checks the shared plant list: every nursery reads it, none can change
// it, and each nursery's own additions stay private to that nursery.
func TestPlantNamesSharedAndOwn(t *testing.T) {
	tx := dbtest.Tx(t)
	ctx := t.Context()
	q := dbgen.New(tx)

	var orgs [2]int64
	for i, slug := range []string{"plant-names-a", "plant-names-b"} {
		org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
		if err != nil {
			t.Fatal(err)
		}
		orgs[i] = org.ID
	}
	a, b := orgs[0], orgs[1]

	var alocasia int64
	if err := tx.QueryRow(ctx, "SELECT id FROM plant_names WHERE rank = 'genus' AND name = 'Alocasia' AND organization_id IS NULL").Scan(&alocasia); err != nil {
		t.Fatal(err)
	}

	err := db.InTenant(ctx, tx, a, func(*dbgen.Queries) error {
		// The shared list is readable.
		var shared int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM plant_names WHERE full_name = 'Alocasia baginda ''Dragon Scale'''").Scan(&shared); err != nil {
			return err
		}
		if shared != 1 {
			t.Errorf("found %d shared Dragon Scale entries, want 1", shared)
		}

		// A nursery can add its own cultivar under a shared genus.
		_, err := tx.Exec(ctx, `INSERT INTO plant_names (organization_id, parent_id, rank, name, full_name)
			VALUES ($1, $2, 'cultivar', 'Root Access Special', 'Alocasia ''Root Access Special''')`, a, alocasia)
		if err != nil {
			t.Errorf("adding own cultivar: %v", err)
		}

		// ...but can't change or delete the shared list (the rows are invisible to writes).
		tag, err := tx.Exec(ctx, "UPDATE plant_names SET name = 'Changed' WHERE id = $1", alocasia)
		if err != nil || tag.RowsAffected() != 0 {
			t.Errorf("updating a shared name: %d rows, %v; want 0 rows", tag.RowsAffected(), err)
		}
		tag, err = tx.Exec(ctx, "DELETE FROM variegations WHERE organization_id IS NULL")
		if err != nil || tag.RowsAffected() != 0 {
			t.Errorf("deleting shared variegations: %d rows, %v; want 0 rows", tag.RowsAffected(), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// A nursery can't add to the shared list or to another nursery's list.
	for _, owner := range []any{nil, b} {
		err := db.InTenant(ctx, tx, a, func(*dbgen.Queries) error {
			_, err := tx.Exec(ctx, `INSERT INTO variegations (organization_id, name) VALUES ($1, 'Sneaky')`, owner)
			return err
		})
		if err == nil {
			t.Errorf("nursery A added a variegation owned by %v, want a row-level security error", owner)
		}
	}

	// Nursery B sees the shared list but not A's addition.
	err = db.InTenant(ctx, tx, b, func(*dbgen.Queries) error {
		var own, shared int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM plant_names WHERE name = 'Root Access Special'").Scan(&own); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM plant_names").Scan(&shared); err != nil {
			return err
		}
		if own != 0 || shared < 200 {
			t.Errorf("nursery B sees %d of A's names and %d names in total; want 0 and the whole shared list", own, shared)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
