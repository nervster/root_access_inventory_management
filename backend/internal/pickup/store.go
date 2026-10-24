package pickup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrNotFound  = errors.New("pickup: not found")
	ErrNameTaken = errors.New("pickup: another window has that name")
)

type Store struct{ conn db.Conn }

func NewStore(conn db.Conn) *Store { return &Store{conn: conn} }

// Rules returns a nursery's pickup rules: its windows, lead time, booking range and time zone.
func (s *Store) Rules(ctx context.Context, orgID int64) (rules Rules, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		rules, err = loadRules(ctx, q, orgID)
		return err
	})
	return rules, err
}

func loadRules(ctx context.Context, q *dbgen.Queries, orgID int64) (Rules, error) {
	org, err := q.GetOrganization(ctx, orgID)
	if err != nil {
		return Rules{}, err
	}
	loc, err := time.LoadLocation(org.TimeZone)
	if err != nil {
		return Rules{}, fmt.Errorf("nursery time zone %q: %w", org.TimeZone, err)
	}
	rules := Rules{LeadHours: DefaultLeadHours, BookingDays: DefaultBookingDays, Location: loc}
	switch settings, err := q.GetPickupSettings(ctx, orgID); {
	case err == nil:
		rules.LeadHours, rules.BookingDays = int(settings.LeadHours), int(settings.BookingDays)
	case !errors.Is(err, pgx.ErrNoRows):
		return Rules{}, err
	}
	windows, err := q.ListPickupWindows(ctx, orgID)
	if err != nil {
		return Rules{}, err
	}
	for _, w := range windows {
		rules.Windows = append(rules.Windows, windowFrom(w))
	}
	return rules, nil
}

func windowFrom(w dbgen.PickupWindow) Window {
	return Window{ID: w.ID, Name: w.Name, StartMinute: int(w.StartMinute), LengthMinutes: int(w.LengthMinutes)}
}

// SetUpWindows replaces the nursery's windows with ones hours long from startHour (GenerateWindows).
// Closures of the old windows go with them; whole-day closures stay.
func (s *Store) SetUpWindows(ctx context.Context, orgID int64, startHour, hours int) error {
	windows, err := GenerateWindows(startHour, hours)
	if err != nil {
		return err
	}
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if err := q.DeletePickupWindows(ctx, orgID); err != nil {
			return err
		}
		for _, w := range windows {
			if _, err := q.CreatePickupWindow(ctx, dbgen.CreatePickupWindowParams{
				OrganizationID: orgID, Name: w.Name, StartMinute: int32(w.StartMinute), LengthMinutes: int32(w.LengthMinutes),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// RenameWindow names a window, or fails with ErrNotFound or ErrNameTaken.
func (s *Store) RenameWindow(ctx context.Context, orgID, windowID int64, name string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.RenamePickupWindow(ctx, dbgen.RenamePickupWindowParams{OrganizationID: orgID, ID: windowID, Name: name})
		return translate(err)
	})
}

// SetLimits sets the lead time and booking range.
func (s *Store) SetLimits(ctx context.Context, orgID int64, leadHours, bookingDays int) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.SetPickupSettings(ctx, dbgen.SetPickupSettingsParams{
			OrganizationID: orgID, LeadHours: int32(leadHours), BookingDays: int32(bookingDays),
		})
		return err
	})
}

// Closures lists the calendar's closures from one date to another, inclusive.
func (s *Store) Closures(ctx context.Context, orgID int64, from, to time.Time) (closures []dbgen.PickupClosure, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		closures, err = q.ListPickupClosures(ctx, dbgen.ListPickupClosuresParams{OrganizationID: orgID, FromDate: from, ToDate: to})
		return err
	})
	return closures, err
}

// Close closes a date (windowID nil) or one window on it; closing it again only updates the reason.
// It fails with ErrNotFound for a window that isn't the nursery's.
func (s *Store) Close(ctx context.Context, orgID int64, date time.Time, windowID *int64, reason *string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.ClosePickup(ctx, dbgen.ClosePickupParams{OrganizationID: orgID, Date: date, WindowID: windowID, Reason: reason})
		return translate(err)
	})
}

// Reopen undoes Close. Reopening what isn't closed is fine.
func (s *Store) Reopen(ctx context.Context, orgID int64, date time.Time, windowID *int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.ReopenPickup(ctx, dbgen.ReopenPickupParams{OrganizationID: orgID, Date: date, WindowID: windowID})
		return err
	})
}

func translate(err error) error {
	var pgErr *pgconn.PgError
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &pgErr) && pgErr.Code == "23505": // unique_violation: the window names
		return ErrNameTaken
	case errors.As(err, &pgErr) && pgErr.Code == "23503": // foreign_key_violation: not this nursery's window
		return ErrNotFound
	}
	return err
}
