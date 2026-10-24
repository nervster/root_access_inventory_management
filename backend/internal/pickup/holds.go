package pickup

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Holds keep a slot for a cart while the shopper pays (docs/pickup-storefront.md).
const (
	HoldDuration       = 30 * time.Minute
	MaxHoldsPerVisitor = 3 // running holds from one visitor's carts, so nobody holds every slot
)

// Why a hold can't be placed or kept: the codes the storefront acts on.
var (
	ErrTaken        = errors.New("taken")
	ErrTooSoon      = errors.New("too_soon")
	ErrClosed       = errors.New("closed")
	ErrTooFar       = errors.New("too_far")
	ErrTooManyHolds = errors.New("too_many_holds")
	ErrHasBookings  = errors.New("pickup: there are upcoming holds or bookings")
)

// Hold is a slot held for a cart.
type Hold struct {
	ID        string // the holdId the cart keeps
	Date      time.Time
	WindowID  int64
	Window    string
	Start     time.Time
	End       time.Time
	ExpiresAt time.Time
}

func holdFrom(b dbgen.PickupBooking) Hold {
	h := Hold{ID: b.HoldToken, Date: b.Date, Window: b.WindowName, Start: b.StartsAt, End: b.EndsAt, ExpiresAt: b.ExpiresAt}
	if b.WindowID != nil {
		h.WindowID = *b.WindowID
	}
	return h
}

// PlaceHold holds the window on date for cartToken until now + HoldDuration, releasing the cart's other
// hold. Holding the slot the cart already holds renews it. It fails with ErrNotFound (no such window),
// ErrClosed, ErrTooSoon, ErrTooFar, ErrTaken, or ErrTooManyHolds (this visitor, by clientIP).
func (s *Store) PlaceHold(ctx context.Context, orgID int64, cartToken, clientIP string, date time.Time, windowID int64, now time.Time) (hold Hold, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		window, err := checkSlot(ctx, q, orgID, date, windowID, now)
		if err != nil {
			return err
		}
		if err := q.ExpirePickupHolds(ctx, dbgen.ExpirePickupHoldsParams{OrganizationID: orgID, Now: now}); err != nil {
			return err
		}

		// One slot per cart: the same one is renewed, another is given back.
		switch current, err := q.CartPickupHold(ctx, dbgen.CartPickupHoldParams{OrganizationID: orgID, CartToken: cartToken, Now: now}); {
		case err == nil && current.Date.Equal(date) && current.WindowID != nil && *current.WindowID == windowID:
			renewed, err := q.RenewPickupHold(ctx, dbgen.RenewPickupHoldParams{OrganizationID: orgID, ID: current.ID, ExpiresAt: now.Add(HoldDuration)})
			hold = holdFrom(renewed)
			return err
		case err == nil:
			if err := q.ReleasePickupHold(ctx, dbgen.ReleasePickupHoldParams{OrganizationID: orgID, ID: current.ID}); err != nil {
				return err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		if clientIP != "" {
			count, err := q.CountVisitorPickupHolds(ctx, dbgen.CountVisitorPickupHoldsParams{OrganizationID: orgID, ClientIp: &clientIP, CartToken: cartToken, Now: now})
			if err != nil {
				return err
			}
			if count >= MaxHoldsPerVisitor {
				return ErrTooManyHolds
			}
		}

		if err := ensureLocker(ctx, q, orgID); err != nil {
			return err
		}
		lockerID, err := q.FreePickupLocker(ctx, dbgen.FreePickupLockerParams{OrganizationID: orgID, Date: date, WindowID: &windowID, Now: now})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTaken
		} else if err != nil {
			return err
		}
		token, err := newHoldToken()
		if err != nil {
			return err
		}
		rules, err := loadRules(ctx, q, orgID)
		if err != nil {
			return err
		}
		start, end := rules.Bounds(date, window)
		var ip *string
		if clientIP != "" {
			ip = &clientIP
		}
		created, err := q.CreatePickupHold(ctx, dbgen.CreatePickupHoldParams{
			OrganizationID: orgID, LockerID: lockerID, Date: date, WindowID: &windowID, WindowName: window.Name,
			StartsAt: start, EndsAt: end, HoldToken: token, CartToken: cartToken, ClientIp: ip, ExpiresAt: now.Add(HoldDuration),
		})
		if isUniqueViolation(err) { // another cart took it in the same moment
			return ErrTaken
		}
		hold = holdFrom(created)
		return err
	})
	return hold, err
}

// RefreshHold keeps cartToken's hold until now + HoldDuration, checking the slot again (still open, still
// starting at least the lead time from now). A hold that ran out is taken back if the slot is still
// free. It fails with ErrNotFound (no such hold for this cart, or released), ErrClosed, ErrTooSoon,
// ErrTooFar, or ErrTaken.
func (s *Store) RefreshHold(ctx context.Context, orgID int64, cartToken, holdID string, now time.Time) (hold Hold, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		current, err := q.PickupHoldByToken(ctx, dbgen.PickupHoldByTokenParams{OrganizationID: orgID, HoldToken: holdID, CartToken: cartToken})
		if errors.Is(err, pgx.ErrNoRows) || err == nil && current.Status != dbgen.PickupBookingStatusHeld && current.Status != dbgen.PickupBookingStatusExpired {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if current.WindowID == nil { // its window was replaced
			return ErrClosed
		}
		if _, err := checkSlot(ctx, q, orgID, current.Date, *current.WindowID, now); err != nil {
			return err
		}
		if err := q.ExpirePickupHolds(ctx, dbgen.ExpirePickupHoldsParams{OrganizationID: orgID, Now: now}); err != nil {
			return err
		}
		renewed, err := q.RenewPickupHold(ctx, dbgen.RenewPickupHoldParams{OrganizationID: orgID, ID: current.ID, ExpiresAt: now.Add(HoldDuration)})
		switch {
		case isUniqueViolation(err): // it ran out and another cart took the slot
			return ErrTaken
		case errors.Is(err, pgx.ErrNoRows):
			return ErrNotFound
		}
		hold = holdFrom(renewed)
		return err
	})
	return hold, err
}

// ReleaseHold gives cartToken's hold back. Releasing a hold that's gone already is fine.
func (s *Store) ReleaseHold(ctx context.Context, orgID int64, cartToken, holdID string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		current, err := q.PickupHoldByToken(ctx, dbgen.PickupHoldByTokenParams{OrganizationID: orgID, HoldToken: holdID, CartToken: cartToken})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		return q.ReleasePickupHold(ctx, dbgen.ReleasePickupHoldParams{OrganizationID: orgID, ID: current.ID})
	})
}

// checkSlot returns the window if the slot can be booked at now as far as the calendar goes (the
// window exists, nothing closed it, the lead time and booking range allow it); holds are checked
// separately.
func checkSlot(ctx context.Context, q *dbgen.Queries, orgID int64, date time.Time, windowID int64, now time.Time) (Window, error) {
	rules, err := loadRules(ctx, q, orgID)
	if err != nil {
		return Window{}, err
	}
	var window Window
	for _, w := range rules.Windows {
		if w.ID == windowID {
			window = w
		}
	}
	if window.ID == 0 {
		return Window{}, ErrNotFound
	}
	closures, err := q.ListPickupClosures(ctx, dbgen.ListPickupClosuresParams{OrganizationID: orgID, FromDate: date, ToDate: date})
	if err != nil {
		return Window{}, err
	}
	closed := false
	for _, cl := range closures {
		if cl.WindowID == nil || *cl.WindowID == windowID {
			closed = true
		}
	}
	switch rules.Status(date, window, closed, now) {
	case StatusClosed:
		return Window{}, ErrClosed
	case StatusTooSoon:
		return Window{}, ErrTooSoon
	case StatusTooFar:
		return Window{}, ErrTooFar
	}
	return window, nil
}

// ensureLocker gives a nursery its first locker the first time a slot is held. (One locker for now;
// more lockers mean more slots per window.)
func ensureLocker(ctx context.Context, q *dbgen.Queries, orgID int64) error {
	lockers, err := q.ListPickupLockers(ctx, orgID)
	if err != nil || len(lockers) > 0 {
		return err
	}
	return q.CreatePickupLocker(ctx, dbgen.CreatePickupLockerParams{OrganizationID: orgID, Name: "Locker 1"})
}

// newHoldToken makes a holdId like "ph_k3j2…": random, so one cart can't guess another's.
func newHoldToken() (string, error) {
	b := make([]byte, 15)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ph_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
