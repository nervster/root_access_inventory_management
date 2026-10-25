package inventory

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func (n *repotNursery) productHistory(as string, productID int64) []productEventResponse {
	n.t.Helper()
	var history []productEventResponse
	_ = json.Unmarshal(n.expect(as, "GET", fmt.Sprintf("/api/orgs/{org}/products/%d/history", productID), "", 200).Body.Bytes(), &history)
	return history
}

// story summarizes a product's history as "type by who details" lines.
func story(history []productEventResponse) string {
	var lines []string
	for _, e := range history {
		who := "?"
		if e.ChangedBy != nil {
			who = strings.TrimSuffix(*e.ChangedBy, "@inventory.example.com")
		}
		lines = append(lines, fmt.Sprintf("%s by %s %s", e.Type, who, e.Details))
	}
	return strings.Join(lines, "\n")
}

// asTenant runs statements as the nursery's database role, acting as userID (0 for nobody).
func (n *repotNursery) asTenant(userID int64, statements string) error {
	user := ""
	if userID != 0 {
		user = fmt.Sprint(userID)
	}
	return pgx.BeginFunc(n.t.Context(), n.db, func(tx pgx.Tx) error {
		_, err := tx.Exec(n.t.Context(), fmt.Sprintf(`
			DO $$ BEGIN
				SET LOCAL ROLE nms_tenant;
				PERFORM set_config('app.organization_id', '%d', true);
				PERFORM set_config('app.user_id', '%s', true);
				%s
			END $$`, n.org.ID, user, statements))
		return err
	})
}

// TestProductHistory changes a product every way the history records, and checks each change is
// one entry (old → new, by name, in order), that changing nothing adds none, and that a Viewer reads it.
func TestProductHistory(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200) // owner: creates the SKU ABDS-SC
	product := n.product(dragonScale)
	if _, err := n.db.Exec(t.Context(), "UPDATE users SET display_name = 'Olivia Owner' WHERE id = $1", n.users["owner"].ID); err != nil {
		t.Fatal(err)
	}

	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":4500}`, 200)
	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":4500}`, 200) // same price again: nothing to record
	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":5200}`, 200)
	n.setVariant("owner", product.ID, n.small.ID, `{"sku":"DS-SMALL"}`, 200)
	productPath := fmt.Sprintf("/api/orgs/{org}/products/%d", product.ID)
	n.expect("owner", "PATCH", productPath, `{"title":"Dragon Scale","description":"Silvery.","productType":"Houseplant","tags":["Aroid","Rare"]}`, 200)
	n.expect("owner", "PATCH", productPath, `{"title":"Dragon Scale","tags":["Aroid","Rare"]}`, 200) // nothing changed

	// Removing a price (back to "No price") isn't offered by the API yet, but the trigger covers it
	// for any way of changing it.
	if err := n.asTenant(n.users["owner"].ID, fmt.Sprintf("UPDATE product_variants SET price_cents = NULL WHERE product_id = %d;", product.ID)); err != nil {
		t.Fatal(err)
	}

	n.expect("owner", "POST", "/api/orgs/{org}/plants/unmark-for-sale", `{"barcodes":["IT-ABDS-000001","IT-ABDS-000002","IT-ABDS-000003","IT-ABDS-000004","IT-ABDS-000005"]}`, 200)
	n.expect("owner", "DELETE", fmt.Sprintf("/api/orgs/{org}/products/%d/variants/%d", product.ID, n.small.ID), "", 204)

	// The deleted SKU's code and pot still read right in the history, and a SKU added later (by
	// marking for sale again) is recorded too.
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)

	want := strings.Join([]string{
		`sku_added by owner {"sku":"ABDS-SC","potSize":"Small Cup","priceCents":null}`, // before Olivia had a name
		`price_changed by Olivia Owner {"to":4500,"sku":"ABDS-SC","from":null,"potSize":"Small Cup"}`,
		`price_changed by Olivia Owner {"to":5200,"sku":"ABDS-SC","from":4500,"potSize":"Small Cup"}`,
		`sku_changed by Olivia Owner {"to":"DS-SMALL","from":"ABDS-SC","potSize":"Small Cup"}`,
		`details_changed by Olivia Owner {"to":"Dragon Scale","from":"Alocasia baginda 'Dragon Scale'","field":"title"}`,
		`details_changed by Olivia Owner {"to":"Silvery.","from":null,"field":"description"}`,
		`details_changed by Olivia Owner {"to":"Houseplant","from":null,"field":"productType"}`,
		`details_changed by Olivia Owner {"to":["Aroid","Rare"],"from":[],"field":"tags"}`,
		`price_changed by Olivia Owner {"to":null,"sku":"DS-SMALL","from":5200,"potSize":"Small Cup"}`,
		`sku_deleted by Olivia Owner {"sku":"DS-SMALL","potSize":"Small Cup","priceCents":null}`,
		`sku_added by Olivia Owner {"sku":"ABDS-SC","potSize":"Small Cup","priceCents":null}`,
	}, "\n")
	history := n.productHistory("viewer", product.ID) // Viewers can read it
	got := story(history)
	if got != want {
		t.Errorf("history:\n%s\n\nwant:\n%s", got, want)
	}

	// Nobody sees another nursery's product history, and unknown products are 404.
	n.expect("rival", "GET", fmt.Sprintf("/api/orgs/{org}/products/%d/history", product.ID), "", 404)
	n.expect("viewer", "GET", "/api/orgs/{org}/products/999999/history", "", 404)
}

// TestProductHistoryKeepsNames checks the name copied into an entry survives its author leaving.
func TestProductHistoryKeepsNames(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)
	product := n.product(dragonScale)
	if _, err := n.db.Exec(t.Context(), "UPDATE users SET display_name = 'Olivia Owner' WHERE id = $1", n.users["owner"].ID); err != nil {
		t.Fatal(err)
	}
	n.setVariant("owner", product.ID, n.small.ID, `{"priceCents":4500}`, 200)
	if _, err := n.db.Exec(t.Context(), "DELETE FROM memberships WHERE user_id = $1", n.users["owner"].ID); err != nil {
		t.Fatal(err)
	}
	history := n.productHistory("viewer", product.ID)
	if last := history[len(history)-1]; last.ChangedBy == nil || *last.ChangedBy != "Olivia Owner" {
		t.Errorf("last entry by %v after Olivia left, want Olivia Owner", last.ChangedBy)
	}
}

// TestProductHistoryCantBeRewritten checks the app's database role can add history but not change it.
func TestProductHistoryCantBeRewritten(t *testing.T) {
	n := newRepotNursery(t)
	n.repot(fmt.Sprintf(`{"potSizeId":%d,"batchId":%d}`, n.small.ID, n.batch.ID), 200)
	n.markForSale(fmt.Sprintf(`{"batchId":%d}`, n.batch.ID), 200)
	for _, statement := range []string{"UPDATE product_events SET type = 'sku_deleted'", "DELETE FROM product_events"} {
		err := n.asTenant(0, statement+";")
		if err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("%q as the nursery's role: %v, want permission denied", statement, err)
		}
	}
}
