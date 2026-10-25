package auth

import (
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

func TestSyncUser(t *testing.T) {
	store := NewStore(dbtest.Tx(t))
	ctx := t.Context()

	created, err := store.SyncUser(ctx, Identity{Subject: "user_test", Email: "  Ann@Example.com ", Name: "Ann"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Email != "ann@example.com" {
		t.Errorf("Email = %q, want it trimmed and lower-cased", created.Email)
	}

	// Signing in again with a new email and no name updates the email and keeps the name.
	updated, err := store.SyncUser(ctx, Identity{Subject: "user_test", Email: "ann@new.example.com"})
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

	// Nothing changed: same user back.
	again, err := store.SyncUser(ctx, Identity{Subject: "user_test", Email: "ann@new.example.com", Name: "Ann"})
	if err != nil {
		t.Fatal(err)
	}
	// Compare fields, not structs: == on a struct compares pointer fields (DisplayName) by address.
	if again.ID != updated.ID || again.Email != updated.Email || again.DisplayName == nil || *again.DisplayName != "Ann" {
		t.Errorf("got %+v, want the same user unchanged", again)
	}
}
