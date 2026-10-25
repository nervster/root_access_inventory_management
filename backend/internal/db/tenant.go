package db

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

type userKey struct{}

// WithUser records in ctx who is making changes (a user ID), so changes made through InTenant are
// attributed to them in history (plant_events.changed_by). auth.SetCurrentUser calls it per request.
func WithUser(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

// Conn is the connection pool, or a transaction in tests. Both can run queries and start a
// (nested) transaction.
type Conn interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// InTenant runs fn in a transaction that can only see and change orgID's data. Postgres enforces
// this (Row-Level Security, see migration 00002), so a query that forgets to filter by
// organization still can't reach another nursery. All work for one nursery goes through here.
func InTenant(ctx context.Context, conn Conn, orgID int64, fn func(q *dbgen.Queries) error) error {
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE nms_tenant"); err != nil {
			return fmt.Errorf("switch to tenant role: %w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.organization_id', $1, true)", strconv.FormatInt(orgID, 10)); err != nil {
			return fmt.Errorf("set tenant: %w", err)
		}
		// Who is making changes, for the history triggers ('' for none, e.g. system jobs).
		user := ""
		if userID, ok := ctx.Value(userKey{}).(int64); ok {
			user = strconv.FormatInt(userID, 10)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.user_id', $1, true)", user); err != nil {
			return fmt.Errorf("set user: %w", err)
		}

		if err := fn(dbgen.New(tx)); err != nil {
			return err
		}

		// Normally the commit ends the role and setting. Inside an outer transaction (tests) they
		// would outlive this function, so undo them.
		if _, err := tx.Exec(ctx, "RESET ROLE"); err != nil {
			return fmt.Errorf("reset role: %w", err)
		}
		_, err := tx.Exec(ctx, "SELECT set_config('app.organization_id', '', true), set_config('app.user_id', '', true)")
		return err
	})
}
