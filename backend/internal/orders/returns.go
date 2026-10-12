package orders

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Returns: a customer sends plants back (requested in Shopify), staff receive them here, check
// them, and the refund is issued in Shopify within a day of receiving. A refund needs the plant
// physically back.

// refundWithin is how soon after a returned plant arrives its refund is due.
const refundWithin = 24 * time.Hour

var ErrNotReturnable = errors.New("that plant isn't one being returned here")

// Return is a return request with its items.
type Return struct {
	dbgen.ListReturnsRow
	Items []dbgen.ListReturnItemsRow
}

// Returns lists returns still needing something (active), or finished ones, newest first.
func (s *Store) Returns(ctx context.Context, orgID int64, active bool) (returns []Return, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		rows, err := q.ListReturns(ctx, dbgen.ListReturnsParams{OrganizationID: orgID, Active: active})
		if err != nil || len(rows) == 0 {
			return err
		}
		ids := make([]int64, len(rows))
		byID := map[int64]int{}
		for i, row := range rows {
			ids[i], byID[row.ID] = row.ID, i
			returns = append(returns, Return{ListReturnsRow: row})
		}
		items, err := q.ListReturnItems(ctx, dbgen.ListReturnItemsParams{OrganizationID: orgID, ReturnIds: ids})
		for _, item := range items {
			i := byID[item.ReturnID]
			returns[i].Items = append(returns[i].Items, item)
		}
		return err
	})
	return returns, err
}

// ReceivePlant records a returned plant as back: it must be one sold with the return's order, for an
// item still waiting for plants. It then waits for a check, and the refund falls due a day after the
// first plant arrives. It fails with ErrNotReturnable.
func (s *Store) ReceivePlant(ctx context.Context, orgID, returnID int64, barcode string) error {
	barcode = strings.ToUpper(strings.TrimSpace(barcode))
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		itemID, err := q.ReturnItemForPlant(ctx, dbgen.ReturnItemForPlantParams{OrganizationID: orgID, ReturnID: returnID, Barcode: barcode})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotReturnable
		} else if err != nil {
			return err
		}
		err = q.ReceiveReturnedPlant(ctx, dbgen.ReceiveReturnedPlantParams{OrganizationID: orgID, Barcode: barcode, ReturnItemID: &itemID})
		if err != nil {
			return err
		}
		return q.MarkReturnReceived(ctx, dbgen.MarkReturnReceivedParams{OrganizationID: orgID, ID: returnID})
	})
}

// CheckReturnedPlant settles a returned plant after its check: back on sale, back to growing, or
// discarded (with an optional reason). It fails with ErrNotFound if the plant isn't waiting for a check.
func (s *Store) CheckReturnedPlant(ctx context.Context, orgID int64, barcode string, to dbgen.PlantStatus, reason *string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.CheckReturnedPlant(ctx, dbgen.CheckReturnedPlantParams{
			OrganizationID: orgID, Barcode: strings.ToUpper(strings.TrimSpace(barcode)), Status: to, Reason: reason,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
}

// UnreturnedRefunds lists refunds issued without a return (so possibly without the plant back) that
// no Owner has approved yet.
func (s *Store) UnreturnedRefunds(ctx context.Context, orgID int64) (refunds []dbgen.UnapprovedRefundsWithoutReturnRow, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		refunds, err = q.UnapprovedRefundsWithoutReturn(ctx, orgID)
		return err
	})
	return refunds, err
}

// ApprovedRefunds lists the refunds without a return approved as special cases, newest first.
func (s *Store) ApprovedRefunds(ctx context.Context, orgID int64) (refunds []dbgen.ApprovedRefundsWithoutReturnRow, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		refunds, err = q.ApprovedRefundsWithoutReturn(ctx, orgID)
		return err
	})
	return refunds, err
}

// ApproveRefund approves a refund without a return as a special case, with a note saying why. It
// fails with ErrNotFound.
func (s *Store) ApproveRefund(ctx context.Context, orgID, refundID int64, note string) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		_, err := q.ApproveRefund(ctx, dbgen.ApproveRefundParams{OrganizationID: orgID, ID: refundID, Note: &note})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
}

// --- Handlers ---

func returnRoutes(org *gin.RouterGroup, h handler) {
	org.GET("/returns", organization.RequirePermission(organization.InventoryView), h.listReturns)
	org.POST("/returns/:returnId/receive", organization.RequirePermission(organization.OrdersPrepare), h.receivePlant)
	org.POST("/returned-plants/:barcode/check", organization.RequirePermission(organization.OrdersPrepare), h.checkReturnedPlant)
	org.GET("/refunds/unreturned", organization.RequirePermission(organization.InventoryView), h.unreturnedRefunds)
	org.GET("/refunds/approved", organization.RequirePermission(organization.InventoryView), h.approvedRefunds)
	org.POST("/refunds/:refundId/approve", organization.RequirePermission(organization.RefundsApprove), h.approveRefund)
}

type returnResponse struct {
	ID            int64                `json:"id"`
	Name          string               `json:"name"` // "#1001-R1"
	OrderName     string               `json:"orderName"`
	ShopifyStatus string               `json:"shopifyStatus"` // REQUESTED, OPEN, CLOSED, DECLINED, CANCELED
	RequestedAt   time.Time            `json:"requestedAt"`
	ReceivedAt    *time.Time           `json:"receivedAt"`  // when the first plant came back
	RefundDueAt   *time.Time           `json:"refundDueAt"` // a day after receiving, until refunded
	RefundOverdue bool                 `json:"refundOverdue"`
	Refunded      bool                 `json:"refunded"`
	AdminURL      string               `json:"adminUrl"` // the order in the Shopify admin, where refunds are issued
	Items         []returnItemResponse `json:"items"`
}

type returnItemResponse struct {
	ID              int64    `json:"id"`
	Title           string   `json:"title"`
	PotSize         *string  `json:"potSize"`
	Quantity        int32    `json:"quantity"`
	Reason          *string  `json:"reason"`
	CustomerNote    *string  `json:"customerNote"`
	SoldBarcodes    []string `json:"soldBarcodes"`    // the order's sold plants: which came back?
	WaitingBarcodes []string `json:"waitingBarcodes"` // received, waiting for a check
	ReceivedCount   int32    `json:"receivedCount"`
}

// listReturns handles GET /orgs/:orgId/returns?show=active (the default) or done.
func (h handler) listReturns(c *gin.Context) {
	show := c.DefaultQuery("show", "active")
	if show != "active" && show != "done" {
		web.Fail(c, http.StatusBadRequest, "show must be active or done")
		return
	}
	returns, err := h.store.Returns(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, show == "active")
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]returnResponse, len(returns))
	for i, r := range returns {
		response[i] = returnResponse{
			ID: r.ID, Name: r.Name, OrderName: r.OrderName, ShopifyStatus: r.ShopifyStatus, RequestedAt: r.RequestedAt,
			ReceivedAt: r.ReceivedAt, Refunded: r.Refunded, AdminURL: adminOrderURL(r.Shop, r.ShopifyOrderID),
			Items: make([]returnItemResponse, len(r.Items)),
		}
		if r.ReceivedAt != nil && !r.Refunded {
			due := r.ReceivedAt.Add(refundWithin)
			response[i].RefundDueAt, response[i].RefundOverdue = &due, time.Now().After(due)
		}
		for j, item := range r.Items {
			response[i].Items[j] = returnItemResponse{
				ID: item.ID, Title: item.Title, PotSize: item.PotSize, Quantity: item.Quantity, Reason: item.Reason,
				CustomerNote: item.CustomerNote, SoldBarcodes: item.SoldBarcodes, WaitingBarcodes: item.WaitingBarcodes,
				ReceivedCount: item.ReceivedCount,
			}
		}
	}
	c.JSON(http.StatusOK, response)
}

// receivePlant handles POST /orgs/:orgId/returns/:returnId/receive {barcode}.
func (h handler) receivePlant(c *gin.Context) {
	returnID, ok := web.IDParam(c, "returnId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "return not found")
		return
	}
	var req struct {
		Barcode string `json:"barcode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Barcode) == "" {
		web.Fail(c, http.StatusBadRequest, `send {"barcode": "…"}`)
		return
	}
	switch err := h.store.ReceivePlant(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, returnID, req.Barcode); {
	case errors.Is(err, ErrNotReturnable):
		web.Fail(c, http.StatusBadRequest, "that plant wasn't sold with this order, or all its returned plants are already back")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

// checkReturnedPlant handles POST /orgs/:orgId/returned-plants/:barcode/check {to, reason}: to is
// for_sale, growing, or discarded (reason optional).
func (h handler) checkReturnedPlant(c *gin.Context) {
	var req struct {
		To     dbgen.PlantStatus `json:"to"`
		Reason string            `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	var reason *string
	switch req.To {
	case dbgen.PlantStatusForSale, dbgen.PlantStatusGrowing:
	case dbgen.PlantStatusDiscarded:
		r := strings.TrimSpace(req.Reason)
		if len(r) > 500 {
			web.Fail(c, http.StatusBadRequest, "the reason must be at most 500 characters")
			return
		}
		if r != "" {
			reason = &r
		}
	default:
		web.Fail(c, http.StatusBadRequest, "to must be for_sale, growing, or discarded")
		return
	}
	switch err := h.store.CheckReturnedPlant(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, c.Param("barcode"), req.To, reason); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "no returned plant waiting for a check with that barcode")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

type refundResponse struct {
	ID          int64     `json:"id"`
	OrderName   string    `json:"orderName"`
	RefundedAt  time.Time `json:"refundedAt"`
	AmountCents *int32    `json:"amountCents"`
	Items       *string   `json:"items"` // e.g. "1 × Dragon Scale Albo"; null for a money-only refund
	AdminURL    string    `json:"adminUrl"`
}

// unreturnedRefunds handles GET /orgs/:orgId/refunds/unreturned.
func (h handler) unreturnedRefunds(c *gin.Context) {
	refunds, err := h.store.UnreturnedRefunds(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]refundResponse, len(refunds))
	for i, f := range refunds {
		response[i] = refundResponse{
			ID: f.ID, OrderName: f.OrderName, RefundedAt: f.RefundedAt, AmountCents: f.AmountCents, Items: f.Items,
			AdminURL: adminOrderURL(f.Shop, f.ShopifyOrderID),
		}
	}
	c.JSON(http.StatusOK, response)
}

type approvedRefundResponse struct {
	refundResponse
	ApprovedAt     time.Time `json:"approvedAt"`
	ApprovedByName *string   `json:"approvedByName"`
	Note           string    `json:"note"` // why it was approved without a return
}

// approvedRefunds handles GET /orgs/:orgId/refunds/approved.
func (h handler) approvedRefunds(c *gin.Context) {
	refunds, err := h.store.ApprovedRefunds(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]approvedRefundResponse, len(refunds))
	for i, f := range refunds {
		response[i] = approvedRefundResponse{
			refundResponse: refundResponse{
				ID: f.ID, OrderName: f.OrderName, RefundedAt: f.RefundedAt, AmountCents: f.AmountCents, Items: f.Items,
				AdminURL: adminOrderURL(f.Shop, f.ShopifyOrderID),
			},
			ApprovedByName: f.ApprovedByName,
		}
		if f.ApprovedAt != nil {
			response[i].ApprovedAt = *f.ApprovedAt
		}
		if f.ApprovalNote != nil {
			response[i].Note = *f.ApprovalNote
		}
	}
	c.JSON(http.StatusOK, response)
}

// approveRefund handles POST /orgs/:orgId/refunds/:refundId/approve {note}: an Owner approves a
// refund without a return as a special case, saying why.
func (h handler) approveRefund(c *gin.Context) {
	refundID, ok := web.IDParam(c, "refundId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "refund not found")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" || len(note) > 1000 {
		web.Fail(c, http.StatusBadRequest, "say why this refund is approved without a return (up to 1000 characters)")
		return
	}
	switch err := h.store.ApproveRefund(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, refundID, note); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "no unapproved refund without a return with that ID")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}
