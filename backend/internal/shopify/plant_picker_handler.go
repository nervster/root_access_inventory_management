package shopify

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// The storefront's plant picker, through the App Proxy (docs/plant-picker-storefront.md).

// maxPlantsPerRequest bounds one hold request; a cart can't hold more than this anyway.
const maxPlantsPerRequest = MaxPlantHoldsPerVisitor

func plantPickerRoutes(proxy *gin.RouterGroup, h handler) {
	proxy.GET("/plants", h.pickablePlants)
	proxy.POST("/plant-holds", h.holdPlants(http.StatusCreated))
	proxy.POST("/plant-holds/refresh", h.holdPlants(http.StatusOK))
	proxy.POST("/plant-holds/release", h.releasePlants)
}

type pickablePlantResponse struct {
	ID    int64  `json:"id"`
	Photo string `json:"photo"` // a link to its latest photo, valid for an hour
	Held  bool   `json:"held"`  // this cart holds it
}

type pickablePlantsResponse struct {
	Variant int64                   `json:"variant"`
	Plants  []pickablePlantResponse `json:"plants"` // empty: none to choose (sold out)
}

// pickablePlants handles GET /apps/plant-stock/plants?variant=…&cartToken=… (proxied): the plants a shopper
// can choose for one SKU, one photo each.
func (h handler) pickablePlants(c *gin.Context) {
	variant, err := strconv.ParseInt(c.Query("variant"), 10, 64)
	cartToken := strings.TrimSpace(c.Query("cartToken"))
	if err != nil || variant <= 0 || len(cartToken) > 200 {
		proxyFail(c, http.StatusBadRequest, "invalid", "variant (the variant's number) is required.")
		return
	}
	plants, err := h.store.PickablePlants(c.Request.Context(), c.GetInt64(proxyOrganizationKey), c.Query("shop"), variant, cartToken, h.now())
	if errors.Is(err, ErrNoSuchVariant) {
		proxyFail(c, http.StatusNotFound, "not_found", "Plants can't be chosen for this item.")
		return
	} else if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := pickablePlantsResponse{Variant: variant, Plants: make([]pickablePlantResponse, len(plants))}
	for i, plant := range plants {
		response.Plants[i] = pickablePlantResponse{ID: plant.ID, Photo: plant.Photo, Held: plant.Held}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, response)
}

type plantHoldRequest struct {
	CartToken string  `json:"cartToken"`
	Plants    []int64 `json:"plants"`
}

// bindPlantHold reads a plant hold request; it answers invalid (and returns false) when cartToken or plants is
// missing, or there are too many plants.
func bindPlantHold(c *gin.Context) (plantHoldRequest, bool) {
	var req plantHoldRequest
	err := c.ShouldBindJSON(&req)
	req.CartToken = strings.TrimSpace(req.CartToken)
	switch {
	case err != nil:
		proxyFail(c, http.StatusBadRequest, "invalid", "The request must be JSON.")
	case req.CartToken == "" || len(req.CartToken) > 200:
		proxyFail(c, http.StatusBadRequest, "invalid", "cartToken is required (the token from /cart.js).")
	case len(req.Plants) == 0 || len(req.Plants) > maxPlantsPerRequest:
		proxyFail(c, http.StatusBadRequest, "invalid", "plants must list 1 to 20 plant IDs.")
	default:
		return req, true
	}
	return req, false
}

type plantHoldResponse struct {
	Plants    []int64   `json:"plants"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// holdPlants handles POST /apps/plant-stock/plant-holds (answering 201) and …/refresh (at Checkout, 200)
// {cartToken, plants}: holds the plants for the cart for 30 minutes, all or none. The same rules either way:
// a refresh takes back holds that ran out, if the plants are still free.
func (h handler) holdPlants(status int) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := bindPlantHold(c)
		if !ok {
			return
		}
		orgID := c.GetInt64(proxyOrganizationKey)
		expiresAt, err := h.store.HoldPlants(c.Request.Context(), orgID, req.CartToken, clientIP(c), req.Plants, h.now())
		var taken *PlantsTakenError
		switch {
		case errors.As(err, &taken):
			c.Header("Cache-Control", "no-store")
			c.JSON(http.StatusConflict, gin.H{
				"error": "taken", "message": "Someone else just chose that plant. Please pick another.", "plants": taken.PlantIDs,
			})
			return
		case errors.Is(err, ErrTooManyPlantHolds):
			proxyFail(c, http.StatusTooManyRequests, "too_many_holds", "Too many plants are being held. Please finish an order first, or try again in 30 minutes.")
			return
		case err != nil:
			web.FailInternal(c, err)
			return
		}
		loc := time.UTC
		if rules, err := h.slots.Rules(c.Request.Context(), orgID); err == nil {
			loc = rules.Location
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(status, plantHoldResponse{Plants: uniqueIDs(req.Plants), ExpiresAt: expiresAt.In(loc)})
	}
}

// releasePlants handles POST /apps/plant-stock/plant-holds/release {cartToken, plants}: gives the plants back
// (lines removed, or the cart emptied).
func (h handler) releasePlants(c *gin.Context) {
	req, ok := bindPlantHold(c)
	if !ok {
		return
	}
	if err := h.store.ReleasePlants(c.Request.Context(), c.GetInt64(proxyOrganizationKey), req.CartToken, req.Plants); err != nil {
		web.FailInternal(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
