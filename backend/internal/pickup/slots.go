// Package pickup decides when customers can collect online orders: each nursery's pickup windows (the
// same every day), its pickup calendar (open by default, with whole days or single windows closed), and
// which slots that leaves. A slot is one window on one date. Lockers, holds and bookings come next.
package pickup

import (
	"fmt"
	"time"
)

// Window is one of the nursery's daily pickup windows, in its time zone. It can run past midnight
// (e.g. 8 PM for 12 hours); it belongs to the date it starts on.
type Window struct {
	ID            int64
	Name          string
	StartMinute   int // after midnight
	LengthMinutes int
}

// Rules are a nursery's pickup rules.
type Rules struct {
	LeadHours   int // a window can be booked only if it starts at least this far ahead
	BookingDays int // customers can book today and this many days in all
	Windows     []Window
	Location    *time.Location
}

// Defaults for a nursery that hasn't changed them (the database's defaults too, migration 00031).
const (
	DefaultLeadHours   = 8
	DefaultBookingDays = 30
)

// A slot's status, for staff and (later) for customers: only open slots can be booked.
const (
	StatusOpen    = "open"
	StatusClosed  = "closed"   // closed on the calendar (the day or this window)
	StatusTaken   = "taken"    // every locker is held or booked
	StatusTooSoon = "too_soon" // starts sooner than the lead time allows, or has started
	StatusTooFar  = "too_far"  // past the booking range
)

// Date is a calendar date (midnight UTC), as Postgres date columns arrive.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// ParseDate reads "2026-10-25".
func ParseDate(text string) (time.Time, error) {
	return time.Parse(time.DateOnly, text)
}

// Today is the date it is now at the nursery.
func (r Rules) Today(now time.Time) time.Time {
	local := now.In(r.Location)
	return Date(local.Year(), local.Month(), local.Day())
}

// Bounds is when a window runs on date, in the nursery's time zone: by the clock on the wall, so a
// window across a daylight-saving change still starts and ends at its usual times.
func (r Rules) Bounds(date time.Time, w Window) (start, end time.Time) {
	start = time.Date(date.Year(), date.Month(), date.Day(), 0, w.StartMinute, 0, 0, r.Location)
	end = time.Date(date.Year(), date.Month(), date.Day(), 0, w.StartMinute+w.LengthMinutes, 0, 0, r.Location)
	return start, end
}

// Status is whether a window on date can be booked now. closed: it's closed on the calendar.
func (r Rules) Status(date time.Time, w Window, closed bool, now time.Time) string {
	start, _ := r.Bounds(date, w)
	switch {
	case closed:
		return StatusClosed
	case start.Before(now.Add(time.Duration(r.LeadHours) * time.Hour)):
		return StatusTooSoon
	case !date.Before(r.Today(now).AddDate(0, 0, r.BookingDays)):
		return StatusTooFar
	}
	return StatusOpen
}

// GenerateWindows makes the day's windows: hours long each, from startHour round the clock. When hours
// doesn't divide 24, the last window is shorter. Each gets a name from its times ("8 AM – 2 PM"),
// which the nursery can change.
func GenerateWindows(startHour, hours int) ([]Window, error) {
	if startHour < 0 || startHour > 23 {
		return nil, fmt.Errorf("the first window must start between 0 and 23 o'clock")
	}
	if hours < 1 || hours > 24 {
		return nil, fmt.Errorf("windows must be 1 to 24 hours long")
	}
	var windows []Window
	for covered := 0; covered < 24; covered += hours {
		length := min(hours, 24-covered)
		w := Window{StartMinute: (startHour + covered) % 24 * 60, LengthMinutes: length * 60}
		w.Name = Label(w)
		windows = append(windows, w)
	}
	return windows, nil
}

// Label names a window by its times: "8 AM – 2 PM", "8 PM – 8 AM (overnight)", or "All day from 8 AM".
func Label(w Window) string {
	if w.LengthMinutes >= 24*60 {
		return "All day from " + clock(w.StartMinute)
	}
	label := clock(w.StartMinute) + " – " + clock((w.StartMinute+w.LengthMinutes)%(24*60))
	if w.StartMinute+w.LengthMinutes > 24*60 {
		label += " (overnight)"
	}
	return label
}

// clock writes minutes after midnight as "8 AM", "12 PM", or "8:30 AM".
func clock(minute int) string {
	h, m := minute/60, minute%60
	suffix := "AM"
	if h >= 12 {
		suffix = "PM"
	}
	h %= 12
	if h == 0 {
		h = 12
	}
	if m == 0 {
		return fmt.Sprintf("%d %s", h, suffix)
	}
	return fmt.Sprintf("%d:%02d %s", h, m, suffix)
}
