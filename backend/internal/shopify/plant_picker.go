package shopify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Shoppers choose the exact plants they buy, by photo (docs/plant-picker-storefront.md). A cart holds the
// plants it chose while the shopper pays, so another shopper can't choose them too.
const (
	PlantHoldDuration       = 30 * time.Minute
	MaxPlantHoldsPerVisitor = 20 // live plant holds from one visitor's carts, so nobody holds all the stock
)

var (
	ErrNoSuchVariant          = errors.New("shopify: no such variant on this store")
	ErrTooManyPlantHolds      = errors.New("shopify: too many plants held")
	errPlantPhotosUnavailable = errors.New("shopify: photo links aren't set up")
)

// PlantsTakenError lists the plants that can't be held: held by another cart, ordered, or no longer for sale
// (or never could be: no photo, or not this nursery's).
type PlantsTakenError struct{ PlantIDs []int64 }

func (e *PlantsTakenError) Error() string {
	return fmt.Sprintf("shopify: plants taken: %v", e.PlantIDs)
}

// PickablePlant is a plant a shopper can choose, with a link to its latest photo (valid for an hour).
type PickablePlant struct {
	ID    int64
	Photo string
	Held  bool // this cart holds it
}

// PickablePlants lists the plants a cart can choose for the store's variant (its number, as the theme knows
// it), oldest first. It fails with ErrNoSuchVariant when Plant Stock didn't put that variant on the store.
func (s *Store) PickablePlants(ctx context.Context, orgID int64, shop string, variant int64, cartToken string, now time.Time) ([]PickablePlant, error) {
	var rows []dbgen.PickablePlantsRow
	err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		variantID, err := q.PickerVariant(ctx, dbgen.PickerVariantParams{
			OrganizationID: orgID, Shop: shop, ShopifyVariantID: fmt.Sprintf("gid://shopify/ProductVariant/%d", variant),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchVariant
		} else if err != nil {
			return err
		}
		rows, err = q.PickablePlants(ctx, dbgen.PickablePlantsParams{OrganizationID: orgID, VariantID: variantID, CartToken: cartToken, Now: now})
		return err
	})
	if err != nil {
		return nil, err
	}
	if s.photos == nil {
		return nil, errPlantPhotosUnavailable
	}
	plants := make([]PickablePlant, len(rows))
	for i, row := range rows {
		photo, err := s.photos.ViewURL(ctx, row.StorageKey)
		if err != nil {
			return nil, err
		}
		plants[i] = PickablePlant{ID: row.ID, Photo: photo, Held: row.Held}
	}
	return plants, nil
}

// HoldPlants holds plantIDs for cartToken until now + PlantHoldDuration, all or none: plants the cart holds
// already (or held until they ran out) are kept, the rest are held anew. It fails with *PlantsTakenError
// listing the plants that can't be held, or ErrTooManyPlantHolds (this visitor, by clientIP).
func (s *Store) HoldPlants(ctx context.Context, orgID int64, cartToken, clientIP string, plantIDs []int64, now time.Time) (expiresAt time.Time, err error) {
	plantIDs = uniqueIDs(plantIDs)
	expiresAt = now.Add(PlantHoldDuration)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		choosable, err := q.LockChoosablePlants(ctx, dbgen.LockChoosablePlantsParams{OrganizationID: orgID, PlantIds: plantIDs})
		if err != nil {
			return err
		}
		if err := q.ExpirePlantHolds(ctx, dbgen.ExpirePlantHoldsParams{OrganizationID: orgID, Now: now}); err != nil {
			return err
		}
		heldElsewhere, err := q.PlantsHeldByOtherCarts(ctx, dbgen.PlantsHeldByOtherCartsParams{
			OrganizationID: orgID, PlantIds: plantIDs, CartToken: cartToken, Now: now,
		})
		if err != nil {
			return err
		}
		var taken []int64
		for _, id := range plantIDs {
			if !slices.Contains(choosable, id) || slices.Contains(heldElsewhere, id) {
				taken = append(taken, id)
			}
		}
		if len(taken) > 0 {
			return &PlantsTakenError{PlantIDs: taken}
		}

		var ip *string
		if clientIP != "" {
			ip = &clientIP
			count, err := q.CountVisitorPlantHolds(ctx, dbgen.CountVisitorPlantHoldsParams{
				OrganizationID: orgID, ClientIp: ip, CartToken: cartToken, PlantIds: plantIDs, Now: now,
			})
			if err != nil {
				return err
			}
			if int(count)+len(plantIDs) > MaxPlantHoldsPerVisitor {
				return ErrTooManyPlantHolds
			}
		}

		renewed, err := q.RenewCartPlantHolds(ctx, dbgen.RenewCartPlantHoldsParams{
			OrganizationID: orgID, CartToken: cartToken, PlantIds: plantIDs, ExpiresAt: expiresAt,
		})
		if err != nil {
			return err
		}
		var fresh []int64
		for _, id := range plantIDs {
			if !slices.Contains(renewed, id) {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) == 0 {
			return nil
		}
		err = q.CreatePlantHolds(ctx, dbgen.CreatePlantHoldsParams{
			OrganizationID: orgID, PlantIds: fresh, CartToken: cartToken, ClientIp: ip, ExpiresAt: expiresAt,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // the lock above makes this unlikely; the rule makes it safe
			return &PlantsTakenError{PlantIDs: fresh}
		}
		return err
	})
	return expiresAt, err
}

// ReleasePlants gives cartToken's holds on plantIDs back. Releasing holds that are gone already is fine.
func (s *Store) ReleasePlants(ctx context.Context, orgID int64, cartToken string, plantIDs []int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		return q.ReleaseCartPlantHolds(ctx, dbgen.ReleaseCartPlantHoldsParams{
			OrganizationID: orgID, CartToken: cartToken, PlantIds: uniqueIDs(plantIDs),
		})
	})
}

func uniqueIDs(ids []int64) []int64 {
	unique := slices.Clone(ids)
	slices.Sort(unique)
	return slices.Compact(unique)
}
