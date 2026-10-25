package shopify

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
)

// PhotoSource reads photos from storage (Google Cloud Storage, or SeaweedFS locally), or a fake in tests.
type PhotoSource interface {
	Open(ctx context.Context, key string) (io.ReadCloser, storage.FileInfo, error)
}

// maxImageBytes is the most Plant Stock uploads per image (Shopify takes up to 20 MB; the app
// shrinks photos far below this).
const maxImageBytes = 20 << 20

// files is the product's images for productSet, in the order chosen: photos already on the store by
// their Shopify ID, new ones uploaded first and sent by where they landed. fresh uploads every photo again (when the store's copy
// was deleted). ok is false when Plant Stock has never managed this product's images and has none
// to add, so whatever images the store has are left alone.
func (p productSync) files(ctx context.Context, fresh bool) (files []map[string]any, ok bool, err error) {
	if len(p.images) == 0 && !p.hasMedia {
		return nil, false, nil
	}
	var toUpload []dbgen.PhotosToSyncRow
	for _, image := range p.images {
		if image.ShopifyMediaID == nil || fresh {
			toUpload = append(toUpload, image)
		}
	}
	uploaded, err := p.upload(ctx, toUpload)
	if err != nil {
		return nil, false, err
	}

	files = []map[string]any{} // empty, not nil: no photos left means none on the store either
	for _, image := range p.images {
		if source, ok := uploaded[image.PhotoID]; ok {
			files = append(files, map[string]any{"originalSource": source, "contentType": "IMAGE"})
		} else {
			// By ID: Shopify keeps the image as it is (by originalSource it would make a new copy).
			files = append(files, map[string]any{"id": *image.ShopifyMediaID})
		}
	}
	return files, true, nil
}

const stageUploadsMutation = `mutation StageUploads($input: [StagedUploadInput!]!) {
  stagedUploadsCreate(input: $input) {
    stagedTargets { url resourceUrl parameters { name value } }
    userErrors { field message }
  }
}`

// upload sends photos to Shopify's storage (Shopify can't fetch them from our private bucket itself),
// returning where each landed, by photo ID, for productSet to attach.
func (p productSync) upload(ctx context.Context, photos []dbgen.PhotosToSyncRow) (map[int64]string, error) {
	uploaded := map[int64]string{}
	if len(photos) == 0 {
		return uploaded, nil
	}

	files := make([]photoFile, len(photos))
	input := make([]map[string]any, len(photos))
	for i, photo := range photos {
		file, err := p.readPhoto(ctx, photo)
		if err != nil {
			return nil, err
		}
		files[i] = file
		input[i] = map[string]any{
			"resource": "IMAGE", "httpMethod": "POST",
			"filename": file.name, "mimeType": file.contentType, "fileSize": strconv.Itoa(len(file.content)),
		}
	}

	var data struct {
		StagedUploadsCreate struct {
			StagedTargets []struct {
				URL         string `json:"url"`
				ResourceURL string `json:"resourceUrl"`
				Parameters  []struct{ Name, Value string }
			} `json:"stagedTargets"`
			UserErrors []struct{ Message string } `json:"userErrors"`
		} `json:"stagedUploadsCreate"`
	}
	if err := p.app.GraphQL(ctx, p.shop, p.token, stageUploadsMutation, map[string]any{"input": input}, &data); err != nil {
		return nil, err
	}
	targets := data.StagedUploadsCreate.StagedTargets
	if len(data.StagedUploadsCreate.UserErrors) > 0 || len(targets) != len(photos) {
		return nil, fmt.Errorf("shopify image upload: %d targets for %d photos %v", len(targets), len(photos), data.StagedUploadsCreate.UserErrors)
	}

	for i, target := range targets {
		params := make(map[string]string, len(target.Parameters))
		order := make([]string, len(target.Parameters))
		for j, param := range target.Parameters {
			params[param.Name], order[j] = param.Value, param.Name
		}
		if err := p.postFile(ctx, target.URL, order, params, files[i].name, files[i].content); err != nil {
			return nil, err
		}
		uploaded[photos[i].PhotoID] = target.ResourceURL
	}
	return uploaded, nil
}

// photoFile is a photo read from storage, ready to upload.
type photoFile struct {
	name, contentType string
	content           []byte
}

func (p productSync) readPhoto(ctx context.Context, photo dbgen.PhotosToSyncRow) (file photoFile, err error) {
	reader, info, err := p.photos.Open(ctx, photo.StorageKey)
	if err != nil {
		return file, fmt.Errorf("photo %d: %w", photo.PhotoID, err)
	}
	defer reader.Close()
	file.content, err = io.ReadAll(io.LimitReader(reader, maxImageBytes+1))
	if err != nil {
		return file, fmt.Errorf("photo %d: %w", photo.PhotoID, err)
	}
	if len(file.content) > maxImageBytes {
		return file, fmt.Errorf("photo %d is over %d MB", photo.PhotoID, maxImageBytes>>20)
	}
	file.contentType = info.ContentType
	extension := "jpg"
	if _, after, found := strings.Cut(info.ContentType, "/"); found && after != "jpeg" {
		extension = after // png, webp, heic, …
	}
	file.name = fmt.Sprintf("plant-photo-%d.%s", photo.PhotoID, extension)
	return file, nil
}

// postFile uploads content as a form, the way the staged target asks: its parameters (in order),
// then the file last.
func (p productSync) postFile(ctx context.Context, url string, order []string, params map[string]string, name string, content []byte) error {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, key := range order {
		if err := form.WriteField(key, params[key]); err != nil {
			return err
		}
	}
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := form.Close(); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	response, err := p.app.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("upload image: %w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(response.Body, 500))
		return fmt.Errorf("upload image: status %d: %s", response.StatusCode, detail)
	}
	return nil
}

// saveMedia records which Shopify image each photo became. productSet returns the product's images
// in the order they were sent; if the counts differ, the IDs are forgotten (to be safe), so the next
// sync uploads them again and replaces whatever is there.
func (p productSync) saveMedia(ctx context.Context, mediaIDs []string) error {
	keep := []int64{}
	if len(mediaIDs) == len(p.images) {
		for i, image := range p.images {
			err := p.q.SaveShopifyMedia(ctx, dbgen.SaveShopifyMediaParams{
				OrganizationID: p.orgID, ProductID: p.product.ID, PhotoID: image.PhotoID, Shop: p.shop, ShopifyMediaID: mediaIDs[i],
			})
			if err != nil {
				return err
			}
			keep = append(keep, image.PhotoID)
		}
	}
	return p.q.ForgetOtherShopifyMedia(ctx, dbgen.ForgetOtherShopifyMediaParams{ProductID: p.product.ID, Shop: p.shop, Keep: keep})
}
