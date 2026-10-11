package shopify

import (
	"fmt"
	"testing"
)

// addPhoto adds a photo of one of the product's plants, stored with content, and returns its ID.
func (s *shopTest) addPhoto(productID int64, content string) int64 {
	s.t.Helper()
	plant := s.addPlants(productID, "Small Cup", "growing", 1)[0]
	key := fmt.Sprintf("orgs/%d/plants/%s/%s.jpg", s.org.ID, plant, content)
	s.photos.files[key] = content
	var id int64
	err := s.db.QueryRow(s.t.Context(), `
		INSERT INTO plant_photos (organization_id, plant_id, storage_key)
		SELECT organization_id, id, $2 FROM plants WHERE barcode = $1
		RETURNING id`, plant, key).Scan(&id)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

// choosePhotos makes these photos the product's images, in this order.
func (s *shopTest) choosePhotos(productID int64, photoIDs ...int64) {
	s.t.Helper()
	ctx := s.t.Context()
	if _, err := s.db.Exec(ctx, "DELETE FROM product_photos WHERE product_id = $1", productID); err != nil {
		s.t.Fatal(err)
	}
	for position, photoID := range photoIDs {
		_, err := s.db.Exec(ctx, `INSERT INTO product_photos (organization_id, product_id, photo_id, position) VALUES ($1, $2, $3, $4)`,
			s.org.ID, productID, photoID, position)
		if err != nil {
			s.t.Fatal(err)
		}
	}
}

// images is the store's product images: their content, in order.
func (s *shopTest) images() []string {
	s.t.Helper()
	var contents []string
	for _, m := range s.onlyProduct().Media {
		contents = append(contents, m.Content)
	}
	return contents
}

func TestSyncImages(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Photographed", map[string]*int{"Small Cup": cents(1200)})
	front, leaf, roots := s.addPhoto(product, "front"), s.addPhoto(product, "leaf"), s.addPhoto(product, "roots")

	// Chosen photos go up in order.
	s.choosePhotos(product, front, leaf)
	s.sync(product)
	if got := s.images(); fmt.Sprint(got) != "[front leaf]" || s.fake.uploadCount != 2 {
		t.Fatalf("images %v after %d uploads, want [front leaf] uploaded once each", got, s.fake.uploadCount)
	}
	frontMedia := s.onlyProduct().Media[0].ID

	// Syncing again (e.g. after a price change) uploads nothing.
	s.sync(product)
	if s.fake.uploadCount != 2 || s.onlyProduct().Media[0].ID != frontMedia {
		t.Errorf("after another sync: %d uploads, want still 2 and the same images", s.fake.uploadCount)
	}

	// Reordering, removing, and adding: only the new photo is uploaded.
	s.choosePhotos(product, roots, front)
	s.sync(product)
	if got := s.images(); fmt.Sprint(got) != "[roots front]" || s.fake.uploadCount != 3 || s.onlyProduct().Media[1].ID != frontMedia {
		t.Errorf("after changing photos: %v with %d uploads, want [roots front], front kept, 3 uploads in all", got, s.fake.uploadCount)
	}

	// Someone deletes an image in Shopify: everything is uploaded again.
	s.onlyProduct().Media = s.onlyProduct().Media[:1]
	s.sync(product)
	if got := s.images(); fmt.Sprint(got) != "[roots front]" || s.fake.uploadCount != 5 {
		t.Errorf("after deleting an image on the store: %v with %d uploads, want [roots front] uploaded again (5)", got, s.fake.uploadCount)
	}
	if statuses := s.productStatuses(); statuses[0].Error != nil {
		t.Errorf("status %+v, want no error", statuses[0])
	}

	// No photos chosen any more: none on the store either.
	s.choosePhotos(product)
	s.sync(product)
	if got := s.images(); len(got) != 0 {
		t.Errorf("after removing all photos: %v, want no images", got)
	}
}

func TestImagesAddedInShopifyAreKept(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Shopify Images", map[string]*int{"Small Cup": cents(1200)})
	s.sync(product)

	// The Owner adds an image in Shopify; Plant Stock has no photos for this product, so it leaves them.
	s.onlyProduct().Media = []fakeMedia{{ID: "gid://shopify/MediaImage/999", Content: "added in Shopify"}}
	s.sync(product)
	if got := s.images(); fmt.Sprint(got) != "[added in Shopify]" {
		t.Errorf("images %v, want the one added in Shopify kept", got)
	}
}

func TestChoosingPhotosQueuesSync(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Queued", map[string]*int{"Small Cup": cents(1200)})
	photo := s.addPhoto(product, "front")
	if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}
	s.choosePhotos(product, photo)
	if n := s.waitingJobs(JobSyncProduct, product); n != 1 {
		t.Errorf("%d product syncs after choosing a photo, want 1", n)
	}
}
