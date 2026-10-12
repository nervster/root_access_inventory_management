package inventory

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// Dashboard is the nursery's inventory at a glance.
type Dashboard struct {
	Statuses  dbgen.PlantStatusCountsRow
	Readiness dbgen.SaleReadinessRow
	PotSizes  []dbgen.PlantsPerPotSizeRow
}

func (s *Store) Dashboard(ctx context.Context, orgID int64) (d Dashboard, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if d.Statuses, err = q.PlantStatusCounts(ctx, orgID); err != nil {
			return err
		}
		if d.Readiness, err = q.SaleReadiness(ctx, orgID); err != nil {
			return err
		}
		d.PotSizes, err = q.PlantsPerPotSize(ctx, orgID)
		return err
	})
	return d, err
}

type dashboardResponse struct {
	Plants            plantCountsJSON       `json:"plants"`
	Last30Days        last30DaysJSON        `json:"last30Days"`
	ForSaleValueCents int64                 `json:"forSaleValueCents"` // the prices of everything for sale now
	ReadyToMark       int64                 `json:"readyToMark"`       // growing, in a sellable pot, priced
	NeedsPrice        int64                 `json:"needsPrice"`        // growing, in a sellable pot, no price yet
	PotSizes          []potSizePipelineJSON `json:"potSizes"`          // smallest first: the growth pipeline
}

type last30DaysJSON struct {
	Received  int64 `json:"received"`
	Discarded int64 `json:"discarded"`
}

type potSizePipelineJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Sellable bool   `json:"sellable"`
	Growing  int64  `json:"growing"`
	ForSale  int64  `json:"forSale"`
}

// dashboard handles GET /dashboard.
func (h handler) dashboard(c *gin.Context) {
	d, err := h.store.Dashboard(c.Request.Context(), organization.CurrentAccess(c).OrganizationID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := dashboardResponse{
		Plants:            plantCountsJSON{Growing: d.Statuses.Growing, ForSale: d.Statuses.ForSale, Held: d.Statuses.Held, Returned: d.Statuses.Returned, Sold: d.Statuses.Sold, Discarded: d.Statuses.Discarded},
		Last30Days:        last30DaysJSON{Received: d.Statuses.ReceivedLast30Days, Discarded: d.Statuses.DiscardedLast30Days},
		ForSaleValueCents: d.Readiness.ForSaleValueCents,
		ReadyToMark:       d.Readiness.ReadyToMark,
		NeedsPrice:        d.Readiness.NeedsPrice,
		PotSizes:          make([]potSizePipelineJSON, len(d.PotSizes)),
	}
	for i, ps := range d.PotSizes {
		response.PotSizes[i] = potSizePipelineJSON(ps)
	}
	c.JSON(http.StatusOK, response)
}
