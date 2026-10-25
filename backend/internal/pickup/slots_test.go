package pickup

import (
	"testing"
	"time"
)

func houston(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestGenerateWindows(t *testing.T) {
	windows, err := GenerateWindows(8, 6)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"8 AM – 2 PM", "2 PM – 8 PM", "8 PM – 2 AM (overnight)", "2 AM – 8 AM"}
	if len(windows) != len(want) {
		t.Fatalf("windows = %+v, want 4", windows)
	}
	for i, w := range windows {
		if w.Name != want[i] || w.LengthMinutes != 360 {
			t.Errorf("window %d = %+v, want %q, 6 hours", i, w, want[i])
		}
	}

	// Hours that don't divide 24: the last window is shorter.
	windows, _ = GenerateWindows(0, 5)
	if last := windows[len(windows)-1]; len(windows) != 5 || last.StartMinute != 20*60 || last.LengthMinutes != 4*60 || last.Name != "8 PM – 12 AM" {
		t.Errorf("5-hour windows = %+v, want 5 with the last 8 PM – 12 AM", windows)
	}

	if windows, _ := GenerateWindows(0, 1); len(windows) != 24 || windows[12].Name != "12 PM – 1 PM" {
		t.Errorf("hourly windows = %d, noon %q; want 24 and 12 PM – 1 PM", len(windows), windows[12].Name)
	}
	if windows, _ := GenerateWindows(8, 24); len(windows) != 1 || windows[0].Name != "All day from 8 AM" {
		t.Errorf("one window = %+v, want All day from 8 AM", windows)
	}
	if _, err := GenerateWindows(24, 6); err == nil {
		t.Error("start hour 24 accepted, want an error")
	}
	if _, err := GenerateWindows(8, 0); err == nil {
		t.Error("0-hour windows accepted, want an error")
	}
}

func TestLeadTime(t *testing.T) {
	loc := houston(t)
	rules := Rules{LeadHours: 8, BookingDays: 30, Location: loc}
	midnight := Window{Name: "12 AM – 8 AM", StartMinute: 0, LengthMinutes: 480}
	tomorrow := Date(2026, 10, 24)

	// At 4:00 PM the 12 AM window is the earliest; at 4:01 PM it's too soon.
	at4 := time.Date(2026, 10, 23, 16, 0, 0, 0, loc)
	if got := rules.Status(tomorrow, midnight, false, at4); got != StatusOpen {
		t.Errorf("at 4:00 PM: %s, want open", got)
	}
	if got := rules.Status(tomorrow, midnight, false, at4.Add(time.Minute)); got != StatusTooSoon {
		t.Errorf("at 4:01 PM: %s, want too_soon", got)
	}
	// A window that has started, or is closed, is never open.
	if got := rules.Status(Date(2026, 10, 23), midnight, false, at4); got != StatusTooSoon {
		t.Errorf("a window that already started: %s, want too_soon", got)
	}
	if got := rules.Status(tomorrow, midnight, true, at4); got != StatusClosed {
		t.Errorf("closed window: %s, want closed", got)
	}
}

func TestBookingRange(t *testing.T) {
	loc := houston(t)
	rules := Rules{LeadHours: 8, BookingDays: 30, Location: loc}
	noon := Window{StartMinute: 12 * 60, LengthMinutes: 60}
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, loc)
	// Today and 29 more days: Oct 1 to Oct 30.
	if got := rules.Status(Date(2026, 10, 30), noon, false, now); got != StatusOpen {
		t.Errorf("day 30: %s, want open", got)
	}
	if got := rules.Status(Date(2026, 10, 31), noon, false, now); got != StatusTooFar {
		t.Errorf("day 31: %s, want too_far", got)
	}
	// "Today" is the nursery's date: 11 PM in Houston is already the next day in UTC.
	late := time.Date(2026, 10, 1, 23, 0, 0, 0, loc)
	if got := rules.Today(late); !got.Equal(Date(2026, 10, 1)) {
		t.Errorf("today at 11 PM Houston = %s, want 2026-10-01", got.Format(time.DateOnly))
	}
}

func TestBoundsOvernightAndDaylightSaving(t *testing.T) {
	loc := houston(t)
	rules := Rules{Location: loc}
	overnight := Window{StartMinute: 20 * 60, LengthMinutes: 12 * 60} // 8 PM – 8 AM

	start, end := rules.Bounds(Date(2026, 10, 23), overnight)
	if !start.Equal(time.Date(2026, 10, 23, 20, 0, 0, 0, loc)) || !end.Equal(time.Date(2026, 10, 24, 8, 0, 0, 0, loc)) {
		t.Errorf("overnight = %s to %s, want Oct 23 8 PM to Oct 24 8 AM", start, end)
	}
	// The night clocks go back (Nov 1, 2026): still 8 PM to 8 AM on the wall, 13 real hours.
	start, end = rules.Bounds(Date(2026, 10, 31), overnight)
	if end.Sub(start) != 13*time.Hour || end.In(loc).Hour() != 8 {
		t.Errorf("across the clock change: %s to %s, want 8 PM to 8 AM (13 hours)", start, end)
	}
}
