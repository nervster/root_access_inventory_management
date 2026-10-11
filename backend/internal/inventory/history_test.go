package inventory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestPlantHistory follows one plant through its life and checks its history tells the story:
// what happened, in order, by whom, with the details.
func TestPlantHistory(t *testing.T) {
	n := newRepotNursery(t) // received into the Tissue Culture Cup by staff
	plantPath := "/api/orgs/{org}/plants/by-barcode/IT-000001"

	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.small.ID), 200)       // staff
	n.markForSale(`{"barcodes":["IT-000001"]}`, 200)                                         // owner
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.tc.ID), 200)          // back to an unsellable pot: off sale too
	photo, _ := n.uploadPhoto("IT-000001", "image/jpeg", 1000, 201)                          // staff, caption "New leaf"
	n.expect("staff", "DELETE", fmt.Sprintf("/api/orgs/{org}/photos/%d", photo.ID), "", 204) //
	n.expect("staff", "POST", "/api/orgs/{org}/plants/discard", `{"barcodes":["IT-000001"],"reason":"root rot"}`, 200)

	var plant plantWithHistoryResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", plantPath, "", 200).Body.Bytes(), &plant)

	var story []string
	for _, e := range plant.History {
		who := "?"
		if e.ChangedBy != nil {
			who = strings.TrimSuffix(*e.ChangedBy, "@inventory.example.com")
		}
		story = append(story, fmt.Sprintf("%s by %s %s", e.Type, who, e.Details))
	}
	want := []string{
		`received by staff {"batch":"B-0001","toPotSize":"Tissue Culture Cup"}`,
		`repotted by staff {"toPotSize":"Small Cup","fromPotSize":"Tissue Culture Cup"}`,
		`marked_for_sale by owner {}`,
		`repotted by staff {"toPotSize":"Tissue Culture Cup","fromPotSize":"Small Cup"}`,
		`unmarked_for_sale by staff {}`,
		fmt.Sprintf(`photo_added by staff {"caption":"New leaf","photoId":%d}`, photo.ID),
		fmt.Sprintf(`photo_deleted by staff {"caption":"New leaf","photoId":%d}`, photo.ID),
		`discarded by staff {"reason":"root rot"}`,
	}
	if strings.Join(story, "\n") != strings.Join(want, "\n") {
		t.Errorf("history:\n%s\n\nwant:\n%s", strings.Join(story, "\n"), strings.Join(want, "\n"))
	}

	// Other plants have their own story, and the other nursery can't read this one.
	var other plantWithHistoryResponse
	_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plants/by-barcode/IT-000002", "", 200).Body.Bytes(), &other)
	if len(other.History) != 1 || other.History[0].Type != "received" {
		t.Errorf("IT-000002's history = %+v, want just its arrival", other.History)
	}
	n.expect("rival", "GET", plantPath, "", 404)
}

// TestHistoryCantBeRewritten checks the app's database role can add history but not change it.
func TestHistoryCantBeRewritten(t *testing.T) {
	n := newRepotNursery(t)
	for _, statement := range []string{"UPDATE plant_events SET type = 'sold'", "DELETE FROM plant_events"} {
		// Each attempt in its own savepoint, so one refusal doesn't abort the test's transaction.
		err := pgx.BeginFunc(t.Context(), n.db, func(tx pgx.Tx) error {
			_, err := tx.Exec(t.Context(), fmt.Sprintf(`
				DO $$ BEGIN
					SET LOCAL ROLE nms_tenant;
					PERFORM set_config('app.organization_id', '%d', true);
					%s;
				END $$`, n.org.ID, statement))
			return err
		})
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%q as the nursery's role: %v, want permission denied", statement, err)
		}
	}
}

// TestHistoryShowsNames checks history names who made each change (their email if they have no
// name), and keeps the name after they leave the nursery.
func TestHistoryShowsNames(t *testing.T) {
	n := newRepotNursery(t) // received by staff, who has no name yet
	if _, err := n.db.Exec(t.Context(), "UPDATE users SET display_name = 'Sam Staff' WHERE id = $1", n.users["staff"].ID); err != nil {
		t.Fatal(err)
	}
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"barcodes":["IT-000001"]}`, n.small.ID), 200) // by staff, now named
	if photo, _ := n.uploadPhoto("IT-000001", "image/jpeg", 1000, 201); photo.UploadedBy == nil || *photo.UploadedBy != "Sam Staff" {
		t.Errorf("photo uploaded by %v, want Sam Staff", photo.UploadedBy)
	}

	who := func() []string {
		var plant plantWithHistoryResponse
		_ = json.Unmarshal(n.expect("viewer", "GET", "/api/orgs/{org}/plants/by-barcode/IT-000001", "", 200).Body.Bytes(), &plant)
		var names []string
		for _, e := range plant.History {
			name := "(no name)"
			if e.ChangedBy != nil {
				name = *e.ChangedBy
			}
			names = append(names, fmt.Sprintf("%s by %s", e.Type, name))
		}
		return names
	}
	want := "received by staff@inventory.example.com, repotted by Sam Staff, photo_added by Sam Staff"
	if got := strings.Join(who(), ", "); got != want {
		t.Errorf("history = %s, want %s (the name as it was at each change)", got, want)
	}

	// Sam leaves the nursery: the history still says who it was.
	if _, err := n.db.Exec(t.Context(), "DELETE FROM memberships WHERE user_id = $1", n.users["staff"].ID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(who(), ", "); got != want {
		t.Errorf("after Sam left, history = %s, want it unchanged", got)
	}
	if photos := n.plantPhotos("viewer", "IT-000001", 200); len(photos) != 1 || photos[0].UploadedBy == nil || *photos[0].UploadedBy != "Sam Staff" {
		t.Errorf("after Sam left, photos = %+v, want still uploaded by Sam Staff", photos)
	}
}
