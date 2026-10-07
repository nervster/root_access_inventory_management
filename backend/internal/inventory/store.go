// Package inventory covers a nursery's plants: pot sizes, tissue-culture batches, each plant's
// barcode and growth, and the products and variants they're sold as.
package inventory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrNameTaken    = errors.New("name is already used")
	ErrInUse        = errors.New("still in use")
	ErrWrongPotList = errors.New("the order must list every pot size exactly once")
)

// Store reads and changes one nursery's inventory. Every method runs through db.InTenant.
type Store struct {
	conn db.Conn
}

func NewStore(conn db.Conn) *Store {
	return &Store{conn: conn}
}

// --- Pot sizes ---

// PotSizes lists orgID's pot sizes, smallest (lowest sort order) first.
func (s *Store) PotSizes(ctx context.Context, orgID int64) (sizes []dbgen.PotSize, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		sizes, err = q.ListPotSizes(ctx, orgID)
		return err
	})
	return sizes, err
}

// CreatePotSize adds a pot size at the end of the list. It fails with ErrNameTaken.
func (s *Store) CreatePotSize(ctx context.Context, params dbgen.CreatePotSizeParams) (size dbgen.PotSize, err error) {
	err = db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		size, err = q.CreatePotSize(ctx, params)
		return translate(err)
	})
	return size, err
}

// UpdatePotSize changes the fields that aren't nil. It fails with ErrNotFound or ErrNameTaken.
func (s *Store) UpdatePotSize(ctx context.Context, params dbgen.UpdatePotSizeParams) (size dbgen.PotSize, err error) {
	err = db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		size, err = q.UpdatePotSize(ctx, params)
		return translate(err)
	})
	return size, err
}

// ReorderPotSizes sets the order of orgID's pot sizes to ids, smallest first. ids must list every
// pot size exactly once (ErrWrongPotList otherwise), so no size is left out of order.
func (s *Store) ReorderPotSizes(ctx context.Context, orgID int64, ids []int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		current, err := q.ListPotSizes(ctx, orgID)
		if err != nil {
			return err
		}
		seen := map[int64]bool{}
		for _, id := range ids {
			seen[id] = true
		}
		if len(ids) != len(current) || len(seen) != len(ids) {
			return ErrWrongPotList
		}
		for i, id := range ids {
			updated, err := q.SetPotSizeOrder(ctx, dbgen.SetPotSizeOrderParams{OrganizationID: orgID, ID: id, SortOrder: int32(i)})
			if err != nil {
				return err
			}
			if updated == 0 { // not one of this nursery's pot sizes
				return ErrWrongPotList
			}
		}
		return nil
	})
}

// DeletePotSize removes a pot size no plant, product, or pot history uses. It fails with
// ErrNotFound or ErrInUse.
func (s *Store) DeletePotSize(ctx context.Context, orgID, id int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		deleted, err := q.DeletePotSize(ctx, dbgen.DeletePotSizeParams{OrganizationID: orgID, ID: id})
		if err != nil {
			return translate(err)
		}
		if deleted == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// translate turns database errors the API reports specially into this package's errors.
func translate(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23503": // foreign_key_violation: something still points here
		return ErrInUse
	}
	return err
}
