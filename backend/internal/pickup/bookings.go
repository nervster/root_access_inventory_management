package pickup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// OrderPickup is what an order's cart chose, from its attributes (docs/pickup-storefront.md).
type OrderPickup struct {
	HoldID string     // Pickup hold
	Date   *time.Time // Pickup date
	Window string     // Pickup window: the window's name
}

// BookOrder turns an order's pickup into its booking, inside the order import's transaction q. Its hold
// becomes the booking; without a usable hold (none, or one Plant Stock doesn't know), the date and window
// it names are booked directly if that slot is free. The lead time isn't checked again: the order is
// paid. It returns a problem for staff ("" when fine): a clash, or no valid slot. Calling it again for a
// booked order changes nothing.
func BookOrder(ctx context.Context, q *dbgen.Queries, orgID, orderID int64, pickup OrderPickup, now time.Time) (problem string, err error) {
	if pickup.HoldID == "" && pickup.Date == nil && pickup.Window == "" {
		return "", nil // not a locker pickup
	}
	if _, err := q.OrderPickupBooking(ctx, dbgen.OrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID}); err == nil {
		return "", nil // booked already
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if err := q.ExpirePickupHolds(ctx, dbgen.ExpirePickupHoldsParams{OrganizationID: orgID, Now: now}); err != nil {
		return "", err
	}

	if pickup.HoldID != "" {
		hold, err := q.PickupBookingByHoldToken(ctx, dbgen.PickupBookingByHoldTokenParams{OrganizationID: orgID, HoldToken: pickup.HoldID})
		switch {
		case err == nil && hold.Status == dbgen.PickupBookingStatusBooked:
			return "Another order already has " + slotName(hold.Date, hold.WindowName) + ".", nil
		case err == nil && hold.Status != dbgen.PickupBookingStatusCancelled:
			_, err := q.BookPickupHold(ctx, dbgen.BookPickupHoldParams{OrganizationID: orgID, ID: hold.ID, OrderID: &orderID})
			switch {
			case errors.Is(err, pgx.ErrNoRows) && hold.WindowID == nil:
				return "Its pickup window (" + slotName(hold.Date, hold.WindowName) + ") was replaced: choose a new one.", nil
			case errors.Is(err, pgx.ErrNoRows): // its hold ran out and someone else took the slot
				return "Another customer has " + slotName(hold.Date, hold.WindowName) + ".", nil
			}
			return "", err
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return "", err
		}
		// A hold Plant Stock doesn't know, or a cancelled one: go by the date and window.
	}

	if pickup.Date == nil || pickup.Window == "" {
		return "The order has no pickup date and window Plant Stock can book.", nil
	}
	rules, err := loadRules(ctx, q, orgID)
	if err != nil {
		return "", err
	}
	var window Window
	for _, w := range rules.Windows {
		if w.Name == pickup.Window {
			window = w
		}
	}
	if window.ID == 0 {
		return fmt.Sprintf("There's no pickup window called %q (%s).", pickup.Window, pickup.Date.Format("Jan 2")), nil
	}
	if err := ensureLocker(ctx, q, orgID); err != nil {
		return "", err
	}
	lockerID, err := q.FreePickupLocker(ctx, dbgen.FreePickupLockerParams{OrganizationID: orgID, Date: *pickup.Date, WindowID: &window.ID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return "Another customer has " + slotName(*pickup.Date, window.Name) + ".", nil
	} else if err != nil {
		return "", err
	}
	token, err := newHoldToken()
	if err != nil {
		return "", err
	}
	start, end := rules.Bounds(*pickup.Date, window)
	_, err = q.CreatePickupBooking(ctx, dbgen.CreatePickupBookingParams{
		OrganizationID: orgID, LockerID: lockerID, Date: *pickup.Date, WindowID: &window.ID, WindowName: window.Name,
		StartsAt: start, EndsAt: end, HoldToken: token, CartToken: fmt.Sprintf("order:%d", orderID), Now: now, OrderID: &orderID,
	})
	return "", err
}

// CancelOrderBooking frees a cancelled order's slot.
func CancelOrderBooking(ctx context.Context, q *dbgen.Queries, orgID, orderID int64) error {
	return q.CancelOrderPickupBooking(ctx, dbgen.CancelOrderPickupBookingParams{OrganizationID: orgID, OrderID: &orderID})
}

// slotName writes a slot for staff: "Oct 24, Morning".
func slotName(date time.Time, window string) string {
	return date.Format("Jan 2") + ", " + window
}
