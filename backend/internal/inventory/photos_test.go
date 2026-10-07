package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
)

// fakeStorage stands in for S3: tests "upload" by adding to files directly.
type fakeStorage struct {
	files   map[string]storage.FileInfo
	deleted []string
}

func (f *fakeStorage) UploadURL(_ context.Context, key, contentType string) (string, error) {
	return "https://storage.test/upload/" + key + "?type=" + contentType, nil
}

func (f *fakeStorage) ViewURL(_ context.Context, key string) (string, error) {
	return "https://storage.test/view/" + key, nil
}

func (f *fakeStorage) Stat(_ context.Context, key string) (storage.FileInfo, bool, error) {
	info, ok := f.files[key]
	return info, ok, nil
}

func (f *fakeStorage) Delete(_ context.Context, key string) error {
	delete(f.files, key)
	f.deleted = append(f.deleted, key)
	return nil
}

// uploadPhoto runs the three upload steps for barcode as staff, "uploading" a file of the given
// type and size, and returns the recorded photo.
func (n *repotNursery) uploadPhoto(barcode, contentType string, size int64, wantStatus int) (photoResponse, string) {
	n.t.Helper()
	var link uploadLinkResponse
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/"+barcode+"/photos/upload-link", `{"contentType":"image/jpeg"}`, 200).Body.Bytes(), &link)
	n.photos.files[link.Key] = storage.FileInfo{Size: size, ContentType: contentType}

	var photo photoResponse
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/"+barcode+"/photos",
		fmt.Sprintf(`{"key":%q,"caption":"New leaf"}`, link.Key), wantStatus).Body.Bytes(), &photo)
	return photo, link.Key
}

func (n *repotNursery) plantPhotos(as, barcode string, wantStatus int) []photoResponse {
	n.t.Helper()
	var photos []photoResponse
	_ = json.Unmarshal(n.expect(as, "GET", "/api/orgs/{org}/plants/by-barcode/"+barcode+"/photos", "", wantStatus).Body.Bytes(), &photos)
	return photos
}

func TestPlantPhotos(t *testing.T) {
	n := newRepotNursery(t)

	// The upload link puts the file in this nursery's folder for this plant.
	var link uploadLinkResponse
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos/upload-link", `{"contentType":"image/jpeg"}`, 200).Body.Bytes(), &link)
	var plantID int64
	if err := n.db.QueryRow(t.Context(), "SELECT id FROM plants WHERE barcode = 'IT-000001' AND organization_id = $1", n.org.ID).Scan(&plantID); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("orgs/%d/plants/%d/", n.org.ID, plantID); !strings.HasPrefix(link.Key, want) || !strings.HasSuffix(link.Key, ".jpg") {
		t.Errorf("key = %q, want it under %s, ending .jpg", link.Key, want)
	}
	// Recording before the file is uploaded is refused.
	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos", fmt.Sprintf(`{"key":%q}`, link.Key), 400)

	first, _ := n.uploadPhoto("IT-000001", "image/jpeg", 400_000, 201)
	second, _ := n.uploadPhoto("it-000001", "image/jpeg", 300_000, 201)
	if first.ViewURL == "" || *first.Caption != "New leaf" || *first.UploadedBy != "staff@inventory.example.com" {
		t.Errorf("photo = %+v, want a view link, the caption, and who uploaded it", first)
	}

	photos := n.plantPhotos("viewer", "IT-000001", 200)
	if len(photos) != 2 || photos[0].ID != second.ID {
		t.Errorf("photos = %+v, want both, newest first", photos)
	}
	if got := n.plantPhotos("viewer", "IT-000002", 200); len(got) != 0 {
		t.Errorf("another plant's photos = %+v, want none", got)
	}

	// Deleting removes the record and the file.
	n.expect("viewer", "DELETE", fmt.Sprintf("/api/orgs/{org}/photos/%d", first.ID), "", 403)
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/photos/%d", first.ID), "", 204)
	if got := n.plantPhotos("viewer", "IT-000001", 200); len(got) != 1 || len(n.photos.deleted) != 1 {
		t.Errorf("after deleting: %d photos, %d files deleted; want 1 and 1", len(got), len(n.photos.deleted))
	}
}

func TestPlantPhotoRules(t *testing.T) {
	n := newRepotNursery(t)

	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos/upload-link", `{"contentType":"text/html"}`, 400)
	n.expect("viewer", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos/upload-link", `{"contentType":"image/jpeg"}`, 403)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-999999/photos/upload-link", `{"contentType":"image/jpeg"}`, 404)

	// Too big, or not really an image (whatever the link was for): refused and the file removed.
	_, key := n.uploadPhoto("IT-000001", "image/jpeg", maxPhotoBytes+1, 400)
	if _, kept := n.photos.files[key]; kept {
		t.Error("the oversized file was kept, want it deleted")
	}
	_, key = n.uploadPhoto("IT-000001", "text/html", 1000, 400)
	if _, kept := n.photos.files[key]; kept {
		t.Error("the non-image file was kept, want it deleted")
	}

	// A file uploaded for one plant can't be claimed for another.
	var link uploadLinkResponse
	_ = json.Unmarshal(n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos/upload-link", `{"contentType":"image/jpeg"}`, 200).Body.Bytes(), &link)
	n.photos.files[link.Key] = storage.FileInfo{Size: 1000, ContentType: "image/jpeg"}
	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000002/photos", fmt.Sprintf(`{"key":%q}`, link.Key), 400)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos", fmt.Sprintf(`{"key":%q}`, link.Key), 201)
	n.expect("staff", "POST", "/api/orgs/{org}/plants/by-barcode/IT-000001/photos", fmt.Sprintf(`{"key":%q}`, link.Key), 409) // saved twice

	// The other nursery can't see or delete them.
	n.plantPhotos("rival", "IT-000001", 404)
	photos := n.plantPhotos("viewer", "IT-000001", 200)
	n.expect("rival", "DELETE", fmt.Sprintf("/api/orgs/{org}/photos/%d", photos[0].ID), "", 404)
}
