package db_test

import (
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

func TestUpsertUser(t *testing.T) {
	q := dbgen.New(dbtest.Tx(t))
	ctx := t.Context()
	name := "Ann"

	created, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_test", Email: "ann@example.com", DisplayName: &name})
	if err != nil {
		t.Fatal(err)
	}

	// Signing in again with a new email and no name updates the email and keeps the name.
	updated, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_test", Email: "ann@new.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if updated.ID != created.ID {
		t.Errorf("ID = %d, want %d (same user)", updated.ID, created.ID)
	}
	if updated.Email != "ann@new.example.com" {
		t.Errorf("Email = %q, want ann@new.example.com", updated.Email)
	}
	if updated.DisplayName == nil || *updated.DisplayName != "Ann" {
		t.Errorf("DisplayName = %v, want Ann", updated.DisplayName)
	}
}
