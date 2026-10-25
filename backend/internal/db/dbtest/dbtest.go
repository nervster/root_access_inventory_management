// Package dbtest gives tests a real, migrated Postgres database (nms_test), created on first use.
// Needs Postgres running: docker compose up -d
package dbtest

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
)

const defaultURL = "postgres://nms:nms_dev@localhost:5433/nms_test?sslmode=disable"

// Pool connects to the test database, creating and migrating it if needed.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultURL
	}
	if err := createDatabase(ctx, url); err != nil {
		t.Fatalf("create test database (is Postgres running? docker compose up -d): %v", err)
	}

	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Tx starts a transaction that is rolled back when the test ends, so tests leave no data behind.
func Tx(t *testing.T) pgx.Tx {
	t.Helper()
	tx, err := Pool(t).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	return tx
}

// createDatabase creates the database named in url if it doesn't exist yet.
func createDatabase(ctx context.Context, url string) error {
	config, err := pgx.ParseConfig(url)
	if err != nil {
		return err
	}
	name := config.Database
	config.Database = "postgres" // connect to the server's default database to create ours

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	// Test packages run in parallel, so another one may have just created it. Postgres reports
	// that as duplicate_database (42P04), or as unique_violation (23505) when both ran at once.
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "42P04" || pgErr.Code == "23505") {
		return nil
	}
	return err
}
