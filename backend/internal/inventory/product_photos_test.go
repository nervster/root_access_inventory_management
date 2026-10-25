package inventory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func (n *repotNursery) candidates(productID int64) []photoCandidateResponse {
	n.t.Helper()
	var photos []photoCandidateResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", fmt.Sprintf("/api/orgs/{org}/products/%d/photo-candidates", productID), "", 200).Body.Bytes(), &photos)
	return photos
}

func (n *repotNursery) setImages(as string, productID int64, ids []int64, wantStatus int) {
	n.t.Helper()
	body, _ := json.Marshal(map[string][]int64{"photoIds": ids})
	n.expect(as, "PUT", fmt.Sprintf("/api/orgs/{org}/products/%d/photos", productID), string(body), wantStatus)
}

func imageIDs(p productResponse) []int64 {
	var ids []int64
	for _, image := range p.Images {
		ids = append(ids, image.PhotoID)
	}
	return ids
}

func TestProductImages(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)
	product := n.product(dragonScale)

	// Three photos of two Dragon Scale plants…
	first, _ := n.uploadPhoto("IT-ABDS-000001", "image/jpeg", 1000, 201)
	second, _ := n.uploadPhoto("IT-ABDS-000001", "image/jpeg", 1000, 201)
	third, _ := n.uploadPhoto("IT-ABDS-000002", "image/jpeg", 1000, 201)
	// …and one of a different plant.
	polly := n.receiveBatch("staff", fmt.Sprintf(`{"plantNameId":%d,"potSizeId":%d,"quantity":1,"receivedOn":"2026-10-02"}`,
		n.plantNameID("Alocasia 'Polly'"), n.small.ID), 201)
	pollyPhoto, _ := n.uploadPhoto(polly.Plants[0].Barcode, "image/jpeg", 1000, 201)

	candidates := n.candidates(product.ID)
	var barcodes []string
	for _, c := range candidates {
		barcodes = append(barcodes, c.Barcode)
		if c.Chosen || c.ViewURL == "" {
			t.Errorf("candidate %+v, want a view link and not chosen yet", c)
		}
	}
	if len(candidates) != 3 || strings.Join(barcodes, ",") != "IT-ABDS-000002,IT-ABDS-000001,IT-ABDS-000001" {
		t.Fatalf("candidates are of %v, want the 3 Dragon Scale photos, newest first (not the Polly)", barcodes)
	}

	// Choose two, in order; then reorder.
	n.setImages("owner", product.ID, []int64{third.ID, first.ID}, 200)
	if got := imageIDs(n.product(dragonScale)); fmt.Sprint(got) != fmt.Sprint([]int64{third.ID, first.ID}) {
		t.Errorf("images = %v, want %d then %d", got, third.ID, first.ID)
	}
	n.setImages("owner", product.ID, []int64{first.ID, second.ID, third.ID}, 200)
	if got := imageIDs(n.product(dragonScale)); fmt.Sprint(got) != fmt.Sprint([]int64{first.ID, second.ID, third.ID}) {
		t.Errorf("images = %v after reordering, want %d, %d, %d", got, first.ID, second.ID, third.ID)
	}
	chosen := 0
	for _, c := range n.candidates(product.ID) {
		if c.Chosen {
			chosen++
		}
	}
	if chosen != 3 {
		t.Errorf("%d candidates marked chosen, want 3", chosen)
	}

	// Refused: another plant's photo, a photo twice, too many, Staff (listings.manage).
	n.setImages("owner", product.ID, []int64{first.ID, pollyPhoto.ID}, 400)
	n.setImages("owner", product.ID, []int64{first.ID, first.ID}, 400)
	n.setImages("owner", product.ID, make([]int64, 21), 400)
	n.setImages("staff", product.ID, []int64{first.ID}, 403)
	n.expect("owner", "PUT", fmt.Sprintf("/api/orgs/{org}/products/%d/photos", product.ID), `{}`, 400) // photoIds missing
	if got := imageIDs(n.product(dragonScale)); len(got) != 3 {
		t.Errorf("images = %v, want the refused requests to change nothing", got)
	}

	// Deleting a plant photo takes it off the product too.
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/photos/%d", second.ID), "", 204)
	if got := imageIDs(n.product(dragonScale)); fmt.Sprint(got) != fmt.Sprint([]int64{first.ID, third.ID}) {
		t.Errorf("images = %v after deleting a photo, want %d, %d", got, first.ID, third.ID)
	}

	n.setImages("owner", product.ID, []int64{}, 200)
	if got := n.product(dragonScale).Images; got == nil || len(got) != 0 {
		t.Errorf("images = %#v after clearing, want an empty list", got)
	}
	n.expect("rival", "GET", fmt.Sprintf("/api/orgs/{org}/products/%d/photo-candidates", product.ID), "", 404)
}
