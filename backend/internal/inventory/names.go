package inventory

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// A nursery's own plant names and variegations, added to the shared list (migration 00003).

var ErrWrongRank = errors.New("that kind of name can't go there")

// NameExistsError means the name is already in the list (shared or the nursery's own).
type NameExistsError struct{ Existing string }

func (e *NameExistsError) Error() string { return e.Existing + " is already in the list" }

// childRanks says what can go under each rank; a family has no parent.
var childRanks = map[dbgen.PlantRank][]dbgen.PlantRank{
	dbgen.PlantRankFamily:  {dbgen.PlantRankGenus},
	dbgen.PlantRankGenus:   {dbgen.PlantRankSpecies, dbgen.PlantRankCultivar}, // a cultivar under a genus is a hybrid
	dbgen.PlantRankSpecies: {dbgen.PlantRankCultivar},
}

// NewPlantName is a name a nursery adds under an existing one.
type NewPlantName struct {
	ParentID *int64 // nil only for a family
	Rank     dbgen.PlantRank
	Name     string
	Notes    *string
}

// AddPlantName adds one of the nursery's own plant names. It fails with ErrPlantNameNotFound (the
// parent), ErrWrongRank, or a *NameExistsError.
func (s *Store) AddPlantName(ctx context.Context, orgID int64, n NewPlantName) (name dbgen.PlantName, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		fullName := n.Name
		if n.ParentID == nil {
			if n.Rank != dbgen.PlantRankFamily {
				return ErrWrongRank
			}
		} else {
			parent, err := q.GetPlantName(ctx, *n.ParentID)
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrPlantNameNotFound
			} else if err != nil {
				return err
			}
			if !slices.Contains(childRanks[parent.Rank], n.Rank) {
				return ErrWrongRank
			}
			switch n.Rank {
			case dbgen.PlantRankSpecies:
				fullName = parent.FullName + " " + n.Name // Alocasia baginda
			case dbgen.PlantRankCultivar:
				fullName = parent.FullName + " '" + n.Name + "'" // Alocasia baginda 'Dragon Scale'
			}
		}

		existing, err := q.PlantNameUnder(ctx, dbgen.PlantNameUnderParams{ParentID: n.ParentID, Name: n.Name})
		if err == nil {
			return &NameExistsError{Existing: existing.FullName}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		name, err = q.CreatePlantName(ctx, dbgen.CreatePlantNameParams{
			OrganizationID: &orgID, ParentID: n.ParentID, Rank: n.Rank, Name: n.Name, FullName: fullName, Notes: n.Notes,
		})
		return err
	})
	return name, err
}

// DeletePlantName removes one of the nursery's own names that nothing uses. It fails with
// ErrNotFound (including for shared names, which can't be deleted) or ErrInUse.
func (s *Store) DeletePlantName(ctx context.Context, orgID, id int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		deleted, err := q.DeletePlantName(ctx, id)
		if err != nil {
			return translate(err)
		}
		if deleted == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// AddVariegation adds one of the nursery's own variegations. It fails with a *NameExistsError.
func (s *Store) AddVariegation(ctx context.Context, orgID int64, name string, notes *string) (variegation dbgen.Variegation, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		existing, err := q.VariegationNamed(ctx, name)
		if err == nil {
			return &NameExistsError{Existing: existing.Name}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		variegation, err = q.CreateVariegation(ctx, dbgen.CreateVariegationParams{OrganizationID: &orgID, Name: name, Notes: notes})
		return err
	})
	return variegation, err
}

// DeleteVariegation removes one of the nursery's own variegations that nothing uses. It fails with
// ErrNotFound or ErrInUse.
func (s *Store) DeleteVariegation(ctx context.Context, orgID, id int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		deleted, err := q.DeleteVariegation(ctx, id)
		if err != nil {
			return translate(err)
		}
		if deleted == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// --- Handlers ---

type addPlantNameRequest struct {
	ParentID *int64          `json:"parentId"` // leave out only for a family
	Rank     dbgen.PlantRank `json:"rank"`     // family, genus, species, or cultivar
	Name     string          `json:"name"`     // just this level: "Moonshine", not the full name
	Notes    *string         `json:"notes"`    // e.g. the trade name
}

// addPlantName handles POST /plant-names.
func (h handler) addPlantName(c *gin.Context) {
	var req addPlantNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	// People often type cultivars with their quotes ('Moonshine'); the full name adds them.
	name := strings.Trim(strings.TrimSpace(req.Name), `'"‘’“”`)
	if name == "" || len(name) > 100 {
		web.Fail(c, http.StatusBadRequest, "name must be 1 to 100 characters")
		return
	}
	if _, known := childRanks[req.Rank]; !known && req.Rank != dbgen.PlantRankCultivar {
		web.Fail(c, http.StatusBadRequest, "rank must be family, genus, species, or cultivar")
		return
	}

	added, err := h.store.AddPlantName(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, NewPlantName{
		ParentID: req.ParentID, Rank: req.Rank, Name: name, Notes: trimmed(req.Notes),
	})
	var exists *NameExistsError
	switch {
	case errors.Is(err, ErrPlantNameNotFound):
		web.Fail(c, http.StatusBadRequest, "the name to put it under wasn't found")
	case errors.Is(err, ErrWrongRank):
		web.Fail(c, http.StatusBadRequest, "a genus goes under a family, a species under a genus, and a cultivar under a species (or a genus, for hybrids)")
	case errors.As(err, &exists):
		web.Fail(c, http.StatusConflict, exists.Error())
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.JSON(http.StatusCreated, plantNameResponse{ID: added.ID, Rank: added.Rank, FullName: added.FullName, Notes: added.Notes, Shared: false})
	}
}

func (h handler) deletePlantName(c *gin.Context) {
	id, ok := web.IDParam(c, "plantNameId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "plant name not found")
		return
	}
	switch err := h.store.DeletePlantName(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, id); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "plant name not found (names from the shared list can't be deleted)")
	case errors.Is(err, ErrInUse):
		web.Fail(c, http.StatusConflict, "plants, batches, products, or other names still use this name")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}

func (h handler) addVariegation(c *gin.Context) {
	var req struct {
		Name  string  `json:"name"`
		Notes *string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 100 {
		web.Fail(c, http.StatusBadRequest, "name must be 1 to 100 characters")
		return
	}
	added, err := h.store.AddVariegation(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, name, trimmed(req.Notes))
	var exists *NameExistsError
	switch {
	case errors.As(err, &exists):
		web.Fail(c, http.StatusConflict, exists.Error())
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.JSON(http.StatusCreated, variegationResponse{ID: added.ID, Name: added.Name, Notes: added.Notes, Shared: false})
	}
}

func (h handler) deleteVariegation(c *gin.Context) {
	id, ok := web.IDParam(c, "variegationId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "variegation not found")
		return
	}
	switch err := h.store.DeleteVariegation(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, id); {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "variegation not found (shared ones can't be deleted)")
	case errors.Is(err, ErrInUse):
		web.Fail(c, http.StatusConflict, "plants, batches, or products still use this variegation")
	case err != nil:
		web.FailInternal(c, err)
	default:
		c.Status(http.StatusNoContent)
	}
}
