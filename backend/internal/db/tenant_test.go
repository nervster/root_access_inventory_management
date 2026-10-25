package db_test

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
)

// TestTenantIsolation works inside nursery A and tries to reach nursery B every way it can.
func TestTenantIsolation(t *testing.T) {
	tx := dbtest.Tx(t)
	ctx := t.Context()
	q := dbgen.New(tx) // owner role: sets up both nurseries

	var orgs [2]dbgen.Organization
	var users [2]dbgen.User
	for i, name := range []string{"a", "b"} {
		org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: "Nursery " + name, Slug: "nursery-" + name, TimeZone: "America/Chicago"})
		if err != nil {
			t.Fatal(err)
		}
		user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_" + name, Email: name + "@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateMembership(ctx, dbgen.CreateMembershipParams{OrganizationID: org.ID, UserID: user.ID, Role: dbgen.OrgRoleOwner}); err != nil {
			t.Fatal(err)
		}
		orgs[i], users[i] = org, user
	}
	a, b := orgs[0], orgs[1]

	err := db.InTenant(ctx, tx, a.ID, func(q *dbgen.Queries) error {
		// Own data is visible.
		if members, err := q.ListMembers(ctx, a.ID); err != nil || len(members) != 1 {
			t.Errorf("own members = %v, %v; want 1 member", members, err)
		}

		// Asking for B by ID finds nothing.
		if members, err := q.ListMembers(ctx, b.ID); err != nil || len(members) != 0 {
			t.Errorf("B's members = %v, %v; want none", members, err)
		}
		if _, err := q.GetOrganization(ctx, b.ID); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("GetOrganization(B) error = %v, want no rows", err)
		}

		// A query with no organization filter at all still sees only A.
		var memberships, people int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM memberships").Scan(&memberships); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users").Scan(&people); err != nil {
			return err
		}
		if memberships != 1 || people != 1 {
			t.Errorf("unfiltered counts: %d memberships, %d users; want 1 and 1 (nursery A only)", memberships, people)
		}

		// Changing B's rows does nothing; they're invisible.
		if err := q.DeleteMember(ctx, dbgen.DeleteMemberParams{OrganizationID: b.ID, ID: users[1].ID}); err != nil {
			return err
		}
		if _, err := q.UpdateOrganization(ctx, dbgen.UpdateOrganizationParams{ID: b.ID, Name: ptr("Hacked")}); !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("UpdateOrganization(B) error = %v, want no rows", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Writing a row into B is rejected outright.
	err = db.InTenant(ctx, tx, a.ID, func(q *dbgen.Queries) error {
		_, err := q.CreateMembership(ctx, dbgen.CreateMembershipParams{OrganizationID: b.ID, UserID: users[0].ID, Role: dbgen.OrgRoleViewer})
		return err
	})
	if err == nil {
		t.Error("inserting a membership into nursery B succeeded, want a row-level security error")
	}

	// A nursery can't change its own status (e.g. un-suspend itself).
	err = db.InTenant(ctx, tx, a.ID, func(*dbgen.Queries) error {
		_, err := tx.Exec(ctx, "UPDATE organizations SET status = 'suspended' WHERE id = $1", a.ID)
		return err
	})
	if err == nil {
		t.Error("updating organizations.status as a tenant succeeded, want permission denied")
	}

	// B is untouched, and outside InTenant the owner role sees everything again.
	if members, err := q.ListMembers(ctx, b.ID); err != nil || len(members) != 1 {
		t.Errorf("B's members afterwards = %v, %v; want its 1 member", members, err)
	}
}

// TestEveryTenantTableHasRowLevelSecurity fails when a table with an organization_id column
// (a nursery-owned table) is added without Row-Level Security.
func TestEveryTenantTableHasRowLevelSecurity(t *testing.T) {
	tx := dbtest.Tx(t)
	rows, err := tx.Query(t.Context(), `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'organization_id' AND NOT a.attisdropped
		WHERE n.nspname = 'public' AND c.relkind = 'r' AND NOT c.relrowsecurity`)
	if err != nil {
		t.Fatal(err)
	}
	unprotected, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if len(unprotected) > 0 {
		t.Errorf("tables without row-level security: %v (see migration 00002 for the pattern)", unprotected)
	}
}

func ptr[T any](v T) *T { return &v }
