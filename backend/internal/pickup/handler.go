package pickup

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// maxCalendarDays is the longest stretch one calendar request returns (a month view plus the days of
// the weeks around it).
const maxCalendarDays = 62

// Routes adds the pickup routes to signedIn, a group behind auth.RequireUser. Anyone who sees inventory
// sees the calendar; those who prepare orders close and reopen windows; Owners and Admins set it up.
func Routes(signedIn *gin.RouterGroup, orgs *organization.Store, store *Store) {
	routes(signedIn, orgs, store, time.Now)
}

func routes(signedIn *gin.RouterGroup, orgs *organization.Store, store *Store, now func() time.Time) {
	h := handler{store: store, now: now}
	org := signedIn.Group("/orgs/:orgId/pickup", organization.RequireMember(orgs))
	view := organization.RequirePermission(organization.InventoryView)
	manage := organization.RequirePermission(organization.PickupManage)
	org.GET("", view, h.getSettings)
	org.PATCH("", manage, h.updateLimits)
	org.PUT("/windows", manage, h.setUpWindows)
	org.PATCH("/windows/:windowId", manage, h.renameWindow)
	org.GET("/calendar", view, h.calendar)
	org.PUT("/closures", organization.RequirePermission(organization.OrdersPrepare), h.setClosure)
}

type handler struct {
	store *Store
	now   func() time.Time // tests set the clock
}

type windowResponse struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	StartMinute   int    `json:"startMinute"` // after midnight, nursery time
	LengthMinutes int    `json:"lengthMinutes"`
}

type settingsResponse struct {
	TimeZone    string           `json:"timeZone"`
	LeadHours   int              `json:"leadHours"`
	BookingDays int              `json:"bookingDays"`
	Windows     []windowResponse `json:"windows"`
}

func newSettingsResponse(rules Rules) settingsResponse {
	windows := make([]windowResponse, len(rules.Windows))
	for i, w := range rules.Windows {
		windows[i] = windowResponse{ID: w.ID, Name: w.Name, StartMinute: w.StartMinute, LengthMinutes: w.LengthMinutes}
	}
	return settingsResponse{TimeZone: rules.Location.String(), LeadHours: rules.LeadHours, BookingDays: rules.BookingDays, Windows: windows}
}

// respondSettings answers with the nursery's settings as they are now.
func (h handler) respondSettings(c *gin.Context) {
	rules, err := h.store.Rules(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, newSettingsResponse(rules))
}

// getSettings handles GET /orgs/:orgId/pickup.
func (h handler) getSettings(c *gin.Context) { h.respondSettings(c) }

type limitsRequest struct {
	LeadHours   *int `json:"leadHours"`
	BookingDays *int `json:"bookingDays"`
}

// updateLimits handles PATCH /orgs/:orgId/pickup: the lead time and booking range.
func (h handler) updateLimits(c *gin.Context) {
	var req limitsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	orgID := organization.CurrentAccess(c).OrganizationID
	rules, err := h.store.Rules(c.Request.Context(), orgID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	lead, days := rules.LeadHours, rules.BookingDays
	if req.LeadHours != nil {
		lead = *req.LeadHours
	}
	if req.BookingDays != nil {
		days = *req.BookingDays
	}
	switch {
	case lead < 0 || lead > 168:
		web.Fail(c, http.StatusBadRequest, "the lead time must be 0 to 168 hours")
		return
	case days < 1 || days > 90:
		web.Fail(c, http.StatusBadRequest, "customers can book 1 to 90 days ahead")
		return
	}
	if err := h.store.SetLimits(c.Request.Context(), orgID, lead, days); err != nil {
		web.FailInternal(c, err)
		return
	}
	h.respondSettings(c)
}

type setUpWindowsRequest struct {
	StartHour *int `json:"startHour"` // 0–23, nursery time
	Hours     *int `json:"hours"`     // each window's length
}

// setUpWindows handles PUT /orgs/:orgId/pickup/windows: replaces the windows with new ones.
func (h handler) setUpWindows(c *gin.Context) {
	var req setUpWindowsRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.StartHour == nil || req.Hours == nil {
		web.Fail(c, http.StatusBadRequest, "startHour and hours are required")
		return
	}
	if _, err := GenerateWindows(*req.StartHour, *req.Hours); err != nil {
		web.Fail(c, http.StatusBadRequest, err.Error())
		return
	}
	switch err := h.store.SetUpWindows(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, *req.StartHour, *req.Hours, h.now()); {
	case errors.Is(err, ErrHasBookings):
		web.Fail(c, http.StatusConflict, "customers have upcoming pickups in the current windows: close single windows or days instead, or wait until they've passed")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	h.respondSettings(c)
}

type renameRequest struct {
	Name string `json:"name"`
}

// renameWindow handles PATCH /orgs/:orgId/pickup/windows/:windowId.
func (h handler) renameWindow(c *gin.Context) {
	windowID, ok := web.IDParam(c, "windowId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "window not found")
		return
	}
	var req renameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 60 {
		web.Fail(c, http.StatusBadRequest, "a window's name must be 1 to 60 characters")
		return
	}
	switch err := h.store.RenameWindow(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, windowID, name); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "window not found")
		return
	case errors.Is(err, ErrNameTaken):
		web.Fail(c, http.StatusConflict, "another window is already called "+name)
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	h.respondSettings(c)
}

type closureResponse struct {
	Reason   *string `json:"reason"`
	ClosedBy *string `json:"closedBy"` // name (email if none), as it was then
}

type bookingResponse struct {
	Status    string    `json:"status"`    // held (a cart, until expiresAt) or booked (an order)
	ExpiresAt time.Time `json:"expiresAt"` // when a hold runs out
}

type slotResponse struct {
	WindowID int64             `json:"windowId"`
	Name     string            `json:"name"`
	Start    time.Time         `json:"start"`
	End      time.Time         `json:"end"`
	Status   string            `json:"status"`   // open, closed, taken, too_soon, too_far
	Closure  *closureResponse  `json:"closure"`  // this window's own closure, if any
	Bookings []bookingResponse `json:"bookings"` // holds still running, and bookings
}

type dayResponse struct {
	Date    string           `json:"date"`    // 2026-10-25
	Closure *closureResponse `json:"closure"` // the whole day's, if closed
	Slots   []slotResponse   `json:"slots"`
}

type calendarResponse struct {
	settingsResponse
	Today string        `json:"today"` // the nursery's date now
	Days  []dayResponse `json:"days"`
}

// calendar handles GET /orgs/:orgId/pickup/calendar?from=2026-10-01&to=2026-10-31: each day's windows
// and whether each can be booked now. Without dates: today and the booking range.
func (h handler) calendar(c *gin.Context) {
	ctx := c.Request.Context()
	orgID := organization.CurrentAccess(c).OrganizationID
	rules, err := h.store.Rules(ctx, orgID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	now := h.now()
	today := rules.Today(now)
	from, to := today, today.AddDate(0, 0, rules.BookingDays-1)
	if text := c.Query("from"); text != "" {
		if from, err = ParseDate(text); err != nil {
			web.Fail(c, http.StatusBadRequest, "from must be a date like 2026-10-25")
			return
		}
	}
	if text := c.Query("to"); text != "" {
		if to, err = ParseDate(text); err != nil {
			web.Fail(c, http.StatusBadRequest, "to must be a date like 2026-10-25")
			return
		}
	}
	if to.Before(from) || to.Sub(from) >= maxCalendarDays*24*time.Hour {
		web.Fail(c, http.StatusBadRequest, "from must be on or before to, at most 62 days apart")
		return
	}
	rules, days, err := h.store.Calendar(ctx, orgID, from, to, now, "")
	if err != nil {
		web.FailInternal(c, err)
		return
	}

	closure := func(cl *dbgen.PickupClosure) *closureResponse {
		if cl == nil {
			return nil
		}
		return &closureResponse{Reason: cl.Reason, ClosedBy: cl.ClosedByName}
	}
	response := calendarResponse{settingsResponse: newSettingsResponse(rules), Today: today.Format(time.DateOnly), Days: []dayResponse{}}
	for _, day := range days {
		d := dayResponse{Date: day.Date.Format(time.DateOnly), Closure: closure(day.Closure), Slots: []slotResponse{}}
		for _, slot := range day.Slots {
			bookings := []bookingResponse{}
			for _, b := range slot.Bookings {
				bookings = append(bookings, bookingResponse{Status: string(b.Status), ExpiresAt: b.ExpiresAt})
			}
			d.Slots = append(d.Slots, slotResponse{
				WindowID: slot.ID, Name: slot.Name, Start: slot.Start, End: slot.End, Status: slot.Status,
				Closure: closure(slot.Closure), Bookings: bookings,
			})
		}
		response.Days = append(response.Days, d)
	}
	c.JSON(http.StatusOK, response)
}

type closureRequest struct {
	Date     string  `json:"date"`     // 2026-10-25
	WindowID *int64  `json:"windowId"` // null: the whole day
	Closed   *bool   `json:"closed"`   // true closes it, false reopens it
	Reason   *string `json:"reason"`   // optional, e.g. "Out of town"
}

// setClosure handles PUT /orgs/:orgId/pickup/closures: closes or reopens a day or one window on it.
func (h handler) setClosure(c *gin.Context) {
	var req closureRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Closed == nil {
		web.Fail(c, http.StatusBadRequest, "date and closed are required")
		return
	}
	date, err := ParseDate(req.Date)
	if err != nil {
		web.Fail(c, http.StatusBadRequest, "date must be a date like 2026-10-25")
		return
	}
	ctx := c.Request.Context()
	orgID := organization.CurrentAccess(c).OrganizationID
	if !*req.Closed {
		err = h.store.Reopen(ctx, orgID, date, req.WindowID)
	} else {
		var reason *string
		if req.Reason != nil {
			if r := strings.TrimSpace(*req.Reason); r != "" {
				if len([]rune(r)) > 200 {
					web.Fail(c, http.StatusBadRequest, "the reason can be at most 200 characters")
					return
				}
				reason = &r
			}
		}
		err = h.store.Close(ctx, orgID, date, req.WindowID, reason)
	}
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "window not found")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}
