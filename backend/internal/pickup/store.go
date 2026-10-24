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

// Day is one date's windows, as the calendar shows them.
type Day struct {
	Date    time.Time
	Closure *dbgen.PickupClosure // the whole day's, if it's closed
	Slots   []Slot
}

// Slot is one window on one date.
type Slot struct {
	Window
	Start, End time.Time
	Status     string               // StatusOpen, StatusClosed, StatusTooSoon, StatusTooFar
	Closure    *dbgen.PickupClosure // this window's own closure on this date, if any
}

// Calendar returns the nursery's rules and each day's windows from one date to another (inclusive),
// with whether each can be booked at now.
func (s *Store) Calendar(ctx context.Context, orgID int64, from, to, now time.Time) (rules Rules, days []Day, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if rules, err = loadRules(ctx, q, orgID); err != nil {
			return err
		}
		closures, err := q.ListPickupClosures(ctx, dbgen.ListPickupClosuresParams{OrganizationID: orgID, FromDate: from, ToDate: to})
		if err != nil {
			return err
		}
		days = buildDays(rules, closures, from, to, now)
		return nil
	})
	return rules, days, err
}

// OpenSlots returns the slots customers can book now: every open one from today through the booking
// range. Every date in the range is listed, those with nothing open too, so a date picker can mark
// them as full.
func (s *Store) OpenSlots(ctx context.Context, orgID int64, now time.Time) (Rules, []Day, error) {
	rules, err := s.Rules(ctx, orgID)
	if err != nil {
		return Rules{}, nil, err
	}
	today := rules.Today(now)
	rules, days, err := s.Calendar(ctx, orgID, today, today.AddDate(0, 0, rules.BookingDays-1), now)
	if err != nil {
		return Rules{}, nil, err
	}
	for i, day := range days {
		open := []Slot{}
		for _, slot := range day.Slots {
			if slot.Status == StatusOpen {
				open = append(open, slot)
			}
		}
		days[i].Slots = open
	}
	return rules, days, nil
}

func buildDays(rules Rules, closures []dbgen.PickupClosure, from, to, now time.Time) []Day {
	type key struct {
		date     string
		windowID int64 // 0: the whole day
	}
	closed := map[key]*dbgen.PickupClosure{}
	for i, cl := range closures {
		k := key{date: cl.Date.Format(time.DateOnly)}
		if cl.WindowID != nil {
			k.windowID = *cl.WindowID
		}
		closed[k] = &closures[i]
	}
	days := []Day{}
	for date := from; !date.After(to); date = date.AddDate(0, 0, 1) {
		day := Day{Date: date, Closure: closed[key{date: date.Format(time.DateOnly)}], Slots: []Slot{}}
		for _, w := range rules.Windows {
			own := closed[key{date: date.Format(time.DateOnly), windowID: w.ID}]
			start, end := rules.Bounds(date, w)
			day.Slots = append(day.Slots, Slot{
				Window: w, Start: start, End: end, Closure: own,
				Status: rules.Status(date, w, day.Closure != nil || own != nil, now),
			})
		}
		days = append(days, day)
	}
	return days
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
