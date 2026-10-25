package shopify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

// potSize returns the nursery's pot size with this name, adding it (in sort order) if needed.
func (s *shopTest) potSize(name string, sortOrder int) int64 {
	s.t.Helper()
	var id int64
	err := s.db.QueryRow(s.t.Context(), `
		INSERT INTO pot_sizes (organization_id, name, sort_order) VALUES ($1, $2, $3)
		ON CONFLICT (organization_id, name) DO UPDATE SET name = excluded.name
		RETURNING id`, s.org.ID, name, sortOrder).Scan(&id)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

// addProduct adds a product for the nth shared plant name, with a variant per pot size: SKU and
// price in cents (nil for unpriced).
func (s *shopTest) addProduct(n int, title string, variants map[string]*int) (productID int64, variantIDs map[string]int64) {
	s.t.Helper()
	ctx := s.t.Context()
	err := s.db.QueryRow(ctx, `
		INSERT INTO products (organization_id, plant_name_id, title, description, product_type, tags)
		SELECT $1, id, $2, 'Easy to grow.'||chr(10)||chr(10)||'Likes <humidity> & light', 'Plant', '{Aroid,Rare}'
		FROM plant_names WHERE organization_id IS NULL AND rank = 'cultivar' ORDER BY id OFFSET $3 LIMIT 1
		RETURNING id`, s.org.ID, title, n).Scan(&productID)
	if err != nil {
		s.t.Fatal(err)
	}
	variantIDs = map[string]int64{}
	for i, pot := range []string{"Small Cup", "Medium Cup"} {
		cents, ok := variants[pot]
		if !ok {
			continue
		}
		var id int64
		sku := fmt.Sprintf("P%d-%s", n, strings.ReplaceAll(pot, " ", ""))
		err := s.db.QueryRow(ctx, `
			INSERT INTO product_variants (organization_id, product_id, pot_size_id, sku, price_cents)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, s.org.ID, productID, s.potSize(pot, i), sku, cents).Scan(&id)
		if err != nil {
			s.t.Fatal(err)
		}
		variantIDs[pot] = id
	}
	return productID, variantIDs
}

func (s *shopTest) setPrice(variantID int64, cents *int) {
	s.t.Helper()
	if _, err := s.db.Exec(s.t.Context(), "UPDATE product_variants SET price_cents = $1 WHERE id = $2", cents, variantID); err != nil {
		s.t.Fatal(err)
	}
}

func (s *shopTest) sync(productID int64) {
	s.t.Helper()
	if err := s.store.SyncProduct(s.t.Context(), s.org.ID, productID); err != nil {
		s.t.Fatalf("sync product %d: %v", productID, err)
	}
}

func (s *shopTest) productStatuses() []productStatusResponse {
	s.t.Helper()
	var statuses []productStatusResponse
	_ = json.Unmarshal(s.expect("owner", "GET", "/api/orgs/{org}/shopify/products", "", 200).Body.Bytes(), &statuses)
	return statuses
}

// waitingJobs counts jobs of kind not yet sent, for one product if productID isn't 0.
func (s *shopTest) waitingJobs(kind string, productID int64) int {
	s.t.Helper()
	var count int
	err := s.db.QueryRow(s.t.Context(), `
		SELECT count(*) FROM jobs
		WHERE organization_id = $1 AND kind = $2 AND started_at IS NULL
		  AND ($3 = 0 OR (payload->>'productId')::bigint = $3)`, s.org.ID, kind, productID).Scan(&count)
	if err != nil {
		s.t.Fatal(err)
	}
	return count
}

func cents(n int) *int { return &n }

func jobFor(orgID int64, kind, payload string) jobs.Job {
	return jobs.Job{OrganizationID: orgID, Kind: kind, Payload: json.RawMessage(payload), Attempt: 1}
}

func TestSyncProduct(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, variants := s.addProduct(0, "Alocasia Dragon Scale", map[string]*int{"Small Cup": cents(1250), "Medium Cup": nil})

	// New to the store: a draft, with only the priced pot size.
	s.sync(product)
	got := s.onlyProduct()
	if got.Status != "DRAFT" || got.Title != "Alocasia Dragon Scale" || got.ProductType != "Plant" || fmt.Sprint(got.Tags) != "[Aroid Rare]" {
		t.Errorf("created %+v, want a draft with the product's details", got)
	}
	if want := "<p>Easy to grow.</p><p>Likes &lt;humidity&gt; &amp; light</p>"; got.DescriptionHTML != want {
		t.Errorf("description = %q, want %q", got.DescriptionHTML, want)
	}
	if len(got.Variants) != 1 || got.Variants[0].SKU != "P0-SmallCup" || got.Variants[0].Price != "12.50" || got.Variants[0].PotSize != "Small Cup" {
		t.Fatalf("variants = %+v, want only the priced Small Cup", got.Variants)
	}
	smallCup := got.Variants[0].ID

	statuses := s.productStatuses()
	numericID := got.ID[strings.LastIndex(got.ID, "/")+1:]
	if len(statuses) != 1 || statuses[0].Error != nil ||
		*statuses[0].AdminURL != "https://admin.shopify.com/store/root-access-test/products/"+numericID {
		t.Errorf("statuses = %+v, want the product linked to the Shopify admin", statuses)
	}

	// The Owner publishes it in Shopify; Plant Stock's changes keep it live and update in place.
	got.Status = "ACTIVE"
	s.setPrice(variants["Medium Cup"], cents(2000))
	s.sync(product)
	got = s.onlyProduct()
	if got.Status != "ACTIVE" || len(got.Variants) != 2 || got.Variants[0].ID != smallCup || got.Variants[1].Price != "20.00" {
		t.Errorf("after pricing Medium Cup: %+v, want it still live, Small Cup kept, Medium Cup added", got)
	}

	// An SKU that loses its price comes off the store.
	s.setPrice(variants["Small Cup"], nil)
	s.sync(product)
	if got = s.onlyProduct(); len(got.Variants) != 1 || got.Variants[0].SKU != "P0-MediumCup" {
		t.Errorf("after unpricing Small Cup: %+v, want only Medium Cup", got.Variants)
	}

	// No prices left: back to draft, the rest left as it was.
	s.setPrice(variants["Medium Cup"], nil)
	s.sync(product)
	if got = s.onlyProduct(); got.Status != "DRAFT" || len(got.Variants) != 1 {
		t.Errorf("with no prices: %+v, want a draft", got)
	}
}

func TestUnpricedProductStaysOff(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "No Price Yet", map[string]*int{"Small Cup": nil})
	s.sync(product)
	if len(s.fake.products) != 0 || len(s.productStatuses()) != 0 {
		t.Errorf("store has %d products, want none", len(s.fake.products))
	}
}

func TestSyncRecreatesADeletedProduct(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Deleted In Shopify", map[string]*int{"Small Cup": cents(900)})
	s.sync(product)
	first := s.onlyProduct()
	first.Status = "ACTIVE"
	delete(s.fake.products, first.ID) // someone deletes it in Shopify

	s.sync(product)
	again := s.onlyProduct()
	if again.ID == first.ID || again.Status != "DRAFT" || len(again.Variants) != 1 {
		t.Errorf("after deleting on the store: %+v, want a new draft", again)
	}
	if statuses := s.productStatuses(); !strings.HasSuffix(*statuses[0].AdminURL, again.ID[strings.LastIndex(again.ID, "/"):]) {
		t.Errorf("admin link %s, want the new product", *statuses[0].AdminURL)
	}
}

func TestSyncRecordsRefusal(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Refused", map[string]*int{"Small Cup": cents(900)})

	s.fake.refuse = "Title is too long"
	s.sync(product) // not an error: retrying wouldn't help
	if statuses := s.productStatuses(); len(statuses) != 1 || statuses[0].Error == nil || *statuses[0].Error != "Title is too long" || statuses[0].AdminURL != nil {
		t.Fatalf("statuses = %+v, want the refusal recorded", statuses)
	}

	s.fake.refuse = ""
	s.sync(product)
	if statuses := s.productStatuses(); statuses[0].Error != nil || statuses[0].AdminURL == nil {
		t.Errorf("after fixing: %+v, want the error cleared", statuses[0])
	}
}

func TestChangesQueueSyncs(t *testing.T) {
	s := newShopTest(t)

	// Without a store, nothing is queued.
	before, variants := s.addProduct(0, "Before Connecting", map[string]*int{"Small Cup": cents(500), "Medium Cup": cents(900)})
	if n := s.waitingJobs(JobSyncProduct, 0); n != 0 {
		t.Fatalf("%d syncs queued without a store, want none", n)
	}

	// Connecting queues pushing everything; the worker's sync_all then queues each priced product.
	s.connect()
	if n := s.waitingJobs(JobSyncAll, 0); n != 1 {
		t.Fatalf("%d sync-alls after connecting, want 1", n)
	}
	unpriced, _ := s.addProduct(1, "Unpriced", map[string]*int{"Small Cup": nil})
	if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil { // start counting afresh
		t.Fatal(err)
	}
	if err := s.store.syncAllJob(t.Context(), jobFor(s.org.ID, JobSyncAll, `{}`)); err != nil {
		t.Fatal(err)
	}
	if s.waitingJobs(JobSyncProduct, before) != 1 || s.waitingJobs(JobSyncProduct, unpriced) != 0 {
		t.Errorf("sync-all queued %d for the priced product and %d for the unpriced one, want 1 and 0",
			s.waitingJobs(JobSyncProduct, before), s.waitingJobs(JobSyncProduct, unpriced))
	}

	// Changing two prices (and the title) while a sync waits adds no second sync.
	s.setPrice(variants["Small Cup"], cents(600))
	s.setPrice(variants["Medium Cup"], cents(1000))
	if _, err := s.db.Exec(t.Context(), "UPDATE products SET title = 'Renamed' WHERE id = $1", before); err != nil {
		t.Fatal(err)
	}
	if n := s.waitingJobs(JobSyncProduct, before); n != 1 {
		t.Errorf("%d syncs waiting after 3 changes, want 1", n)
	}

	// Once that one is sent, the next change queues another; so does renaming a pot size.
	if _, err := s.db.Exec(t.Context(), "UPDATE jobs SET started_at = now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(t.Context(), "UPDATE pot_sizes SET name = '4 inch' WHERE name = 'Medium Cup'"); err != nil {
		t.Fatal(err)
	}
	if n := s.waitingJobs(JobSyncProduct, before); n != 1 {
		t.Errorf("%d syncs after renaming a pot size, want 1", n)
	}
}

func TestSyncNow(t *testing.T) {
	s := newShopTest(t)
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/sync", "", 404) // no store yet
	s.connect()
	if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/sync", "", 202)
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/sync", "", 202) // clicking twice queues it once
	if n := s.waitingJobs(JobSyncAll, 0); n != 1 {
		t.Errorf("%d sync-alls queued, want 1", n)
	}
	s.expect("admin", "POST", "/api/orgs/{org}/shopify/sync", "", 403)
	s.expect("admin", "GET", "/api/orgs/{org}/shopify/products", "", 403)
}

func TestDescriptionHTML(t *testing.T) {
	for input, want := range map[string]string{
		"":                       "",
		"  ":                     "",
		"One line":               "<p>One line</p>",
		"Two\nlines":             "<p>Two<br>lines</p>",
		"Para one\r\n\r\nPara 2": "<p>Para one</p><p>Para 2</p>",
		`<script>"x"</script>`:   "<p>&lt;script&gt;&#34;x&#34;&lt;/script&gt;</p>",
	} {
		if got := descriptionHTML(&input); got != want {
			t.Errorf("descriptionHTML(%q) = %q, want %q", input, got, want)
		}
	}
}
