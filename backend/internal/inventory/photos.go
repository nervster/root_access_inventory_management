package inventory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/web"
)

// PhotoStorage keeps photo files. *storage.S3 is the real one (Google Cloud Storage, or SeaweedFS locally).
type PhotoStorage interface {
	UploadURL(ctx context.Context, key, contentType string) (string, error)
	ViewURL(ctx context.Context, key string) (string, error)
	Stat(ctx context.Context, key string) (storage.FileInfo, bool, error)
	Delete(ctx context.Context, key string) error
}

const maxPhotoBytes = 10 << 20 // 10 MB; the app shrinks photos well below this before uploading

// photoTypes are the image types accepted, with the file extension used for each.
var photoTypes = map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}

// photoFolder is where a plant's photos are kept: each nursery and plant has its own folder.
func photoFolder(orgID, plantID int64) string {
	return fmt.Sprintf("orgs/%d/plants/%d/", orgID, plantID)
}

// --- Store ---

// PlantByBarcodeID returns the ID of the plant with barcode, or ErrNotFound.
func (s *Store) PlantByBarcodeID(ctx context.Context, orgID int64, barcode string) (plantID int64, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		plant, err := q.GetPlantByBarcode(ctx, dbgen.GetPlantByBarcodeParams{OrganizationID: orgID, Barcode: strings.ToUpper(strings.TrimSpace(barcode))})
		plantID = plant.ID
		return translate(err)
	})
	return plantID, err
}

func (s *Store) AddPlantPhoto(ctx context.Context, params dbgen.CreatePlantPhotoParams) (photo dbgen.PlantPhoto, err error) {
	err = db.InTenant(ctx, s.conn, params.OrganizationID, func(q *dbgen.Queries) error {
		photo, err = q.CreatePlantPhoto(ctx, params)
		return translate(err)
	})
	return photo, err
}

// PlantPhotos lists a plant's photos, newest first.
func (s *Store) PlantPhotos(ctx context.Context, orgID, plantID int64) (photos []dbgen.PlantPhoto, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		photos, err = q.ListPlantPhotos(ctx, dbgen.ListPlantPhotosParams{OrganizationID: orgID, PlantID: plantID})
		return err
	})
	return photos, err
}

// DeletePlantPhoto removes a photo's record and returns where its file is, or fails with ErrNotFound.
func (s *Store) DeletePlantPhoto(ctx context.Context, orgID, photoID int64) (storageKey string, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		storageKey, err = q.DeletePlantPhoto(ctx, dbgen.DeletePlantPhotoParams{OrganizationID: orgID, ID: photoID})
		return translate(err)
	})
	return storageKey, err
}

// --- Handlers ---
//
// Uploading takes three steps, so files go straight from the phone to storage:
//  1. POST …/photos/upload-link: the API picks where the file goes and signs a 15-minute upload link.
//  2. The browser PUTs the file to that link.
//  3. POST …/photos: the API checks the file arrived (in this plant's folder, an image, not too big)
//     and records it.

// plantFromRoute resolves :barcode to a plant ID, writing a 404 if there's no such plant.
func (h handler) plantFromRoute(c *gin.Context) (int64, bool) {
	plantID, err := h.store.PlantByBarcodeID(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, c.Param("barcode"))
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "no plant with that barcode")
		return 0, false
	case err != nil:
		web.FailInternal(c, err)
		return 0, false
	}
	return plantID, true
}

type uploadLinkResponse struct {
	UploadURL   string `json:"uploadUrl"` // PUT the file here, with this Content-Type, within 15 minutes
	ContentType string `json:"contentType"`
	Key         string `json:"key"` // send back when recording the photo
	MaxBytes    int    `json:"maxBytes"`
}

func (h handler) photoUploadLink(c *gin.Context) {
	var req struct {
		ContentType string `json:"contentType"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	extension, ok := photoTypes[req.ContentType]
	if !ok {
		web.Fail(c, http.StatusBadRequest, "photos must be JPEG, PNG, or WebP")
		return
	}
	plantID, ok := h.plantFromRoute(c)
	if !ok {
		return
	}

	key := photoFolder(organization.CurrentAccess(c).OrganizationID, plantID) + uuid.NewString() + "." + extension
	url, err := h.photos.UploadURL(c.Request.Context(), key, req.ContentType)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	c.JSON(http.StatusOK, uploadLinkResponse{UploadURL: url, ContentType: req.ContentType, Key: key, MaxBytes: maxPhotoBytes})
}

type photoResponse struct {
	ID         int64     `json:"id"`
	ViewURL    string    `json:"viewUrl"` // valid for an hour
	TakenAt    time.Time `json:"takenAt"`
	Caption    *string   `json:"caption"`
	UploadedBy *string   `json:"uploadedBy"` // their name (email if they have none), as it was then
}

type addPhotoRequest struct {
	Key     string     `json:"key"`
	Caption *string    `json:"caption"`
	TakenAt *time.Time `json:"takenAt"` // defaults to now
}

func (h handler) addPlantPhoto(c *gin.Context) {
	access := organization.CurrentAccess(c)
	var req addPhotoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		web.Fail(c, http.StatusBadRequest, "request body must be JSON")
		return
	}
	plantID, ok := h.plantFromRoute(c)
	if !ok {
		return
	}
	// Only files in this plant's own folder, so nobody can claim another plant's or nursery's file.
	if !strings.HasPrefix(req.Key, photoFolder(access.OrganizationID, plantID)) || strings.Contains(req.Key, "..") {
		web.Fail(c, http.StatusBadRequest, "that file isn't this plant's upload")
		return
	}

	ctx := c.Request.Context()
	info, exists, err := h.photos.Stat(ctx, req.Key)
	switch {
	case err != nil:
		web.FailInternal(c, err)
		return
	case !exists:
		web.Fail(c, http.StatusBadRequest, "the photo hasn't been uploaded yet")
		return
	case info.Size > maxPhotoBytes || photoTypes[info.ContentType] == "":
		h.deleteFile(ctx, req.Key)
		web.Fail(c, http.StatusBadRequest, "photos must be JPEG, PNG, or WebP images of at most 10 MB")
		return
	}

	user := auth.CurrentUser(c)
	photo, err := h.store.AddPlantPhoto(ctx, dbgen.CreatePlantPhotoParams{
		OrganizationID: access.OrganizationID, PlantID: plantID, StorageKey: req.Key,
		TakenAt: req.TakenAt, Caption: trimmed(req.Caption), UploadedBy: &user.ID,
	})
	switch {
	case errors.Is(err, ErrNameTaken): // the key is unique: this file is already recorded
		web.Fail(c, http.StatusConflict, "this photo is already saved")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	viewURL, err := h.photos.ViewURL(ctx, photo.StorageKey)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	c.JSON(http.StatusCreated, photoResponse{ID: photo.ID, ViewURL: viewURL, TakenAt: photo.TakenAt, Caption: photo.Caption, UploadedBy: photo.UploadedByName})
}

func (h handler) listPlantPhotos(c *gin.Context) {
	plantID, ok := h.plantFromRoute(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	photos, err := h.store.PlantPhotos(ctx, organization.CurrentAccess(c).OrganizationID, plantID)
	if err != nil {
		web.FailInternal(c, err)
		return
	}
	response := make([]photoResponse, len(photos))
	for i, p := range photos {
		viewURL, err := h.photos.ViewURL(ctx, p.StorageKey)
		if err != nil {
			web.FailInternal(c, err)
			return
		}
		response[i] = photoResponse{ID: p.ID, ViewURL: viewURL, TakenAt: p.TakenAt, Caption: p.Caption, UploadedBy: p.UploadedByName}
	}
	c.JSON(http.StatusOK, response)
}

func (h handler) deletePlantPhoto(c *gin.Context) {
	photoID, ok := web.IDParam(c, "photoId")
	if !ok {
		web.Fail(c, http.StatusNotFound, "photo not found")
		return
	}
	key, err := h.store.DeletePlantPhoto(c.Request.Context(), organization.CurrentAccess(c).OrganizationID, photoID)
	switch {
	case errors.Is(err, ErrNotFound):
		web.Fail(c, http.StatusNotFound, "photo not found")
		return
	case err != nil:
		web.FailInternal(c, err)
		return
	}
	h.deleteFile(c.Request.Context(), key)
	c.Status(http.StatusNoContent)
}

// deleteFile removes a file, logging rather than failing: the record is what matters, and an
// orphaned file only costs storage.
func (h handler) deleteFile(ctx context.Context, key string) {
	if err := h.photos.Delete(ctx, key); err != nil {
		slog.Error("delete photo file", "key", key, "error", err)
	}
}
