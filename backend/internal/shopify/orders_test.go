package shopify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/inventory"
)

// placeOrder adds an order to the store: each item is a variant of the store's only product
// (by pot size) and a quantity.
func (s *shopTest) placeOrder(name string, items map[string]int) map[string]any {
	s.t.Helper()
	var lines []map[string]any
	for _, v := range s.onlyProduct().Variants {
		if quantity, ok := items[v.PotSize]; ok {
			lines = append(lines, map[string]any{
				"id": s.fake.id("LineItem"), "title": s.onlyProduct().Title + " - " + v.PotSize, "sku": v.SKU,
				"quantity": quantity, "variant": map[string]string{"id": v.ID},
				"originalUnitPriceSet": map[string]any{"shopMoney": map[string]string{"amount": v.Price}},
			})
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	order := map[string]any{
		"id": s.fake.id("Order"), "name": name, "createdAt": now, "updatedAt": now, "cancelledAt": nil,
		"displayFulfillmentStatus": "UNFULFILLED", "lineItems": map[string]any{"nodes": lines},
	}
	s.fake.orders = append(s.fake.orders, order)
	return order
}

func (s *shopTest) importOrders() {
	s.t.Helper()
	if err := s.store.ImportOrders(s.t.Context(), s.org.ID); err != nil {
		s.t.Fatalf("import orders: %v", err)
	}
}

type orderJSON struct {
	Name, Status string
	Items        []struct {
		Title         string
		PotSize       *string
		Quantity      int
		PriceCents    *int
		InPlantStock  bool
		HeldBarcodes  []string
		ChosenBarcode *string
	}
}

// ordersWith lists the nursery's orders with status, as staff see them.
func (s *shopTest) ordersWith(status string) []orderJSON {
	s.t.Helper()
	var orders []orderJSON
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", fmt.Sprintf("/api/orgs/%d/orders?status=%s", s.org.ID, status), nil)
	s.ordersRouter("admin").ServeHTTP(recorder, request)
	if recorder.Code != 200 {
		s.t.Fatalf("list orders: %d %s", recorder.Code, recorder.Body)
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &orders)
	return orders
}

func (s *shopTest) plantStatus(barcode string) string {
	s.t.Helper()
	var status string
	if err := s.db.QueryRow(s.t.Context(), "SELECT status FROM plants WHERE barcode = $1", barcode).Scan(&status); err != nil {
		s.t.Fatal(err)
	}
	return status
}

func TestOrderHoldsPlants(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Ordered", map[string]*int{"Small Cup": cents(1500), "Medium Cup": cents(2500)})
	oldest := s.addPlants(product, "Small Cup", "for_sale", 2)
	newer := s.addPlants(product, "Small Cup", "for_sale", 1)
	s.addPlants(product, "Medium Cup", "for_sale", 1)
	s.sync(product)

	s.placeOrder("#1001", map[string]int{"Small Cup": 2, "Medium Cup": 2}) // only 1 Medium Cup for sale
	s.importOrders()

	orders := s.ordersWith("open")
	if len(orders) != 1 || orders[0].Name != "#1001" || len(orders[0].Items) != 2 {
		t.Fatalf("open orders = %+v, want #1001 with 2 items", orders)
	}
	small, medium := orders[0].Items[0], orders[0].Items[1]
	slices.Sort(oldest) // listed by barcode
	if *small.PotSize != "Small Cup" || *small.PriceCents != 1500 || fmt.Sprint(small.HeldBarcodes) != fmt.Sprint(oldest) {
		t.Errorf("Small Cup item = %+v, want the 2 oldest plants held (%v)", small, oldest)
	}
	if len(medium.HeldBarcodes) != 1 || medium.Quantity != 2 {
		t.Errorf("Medium Cup item = %+v, want 1 of 2 held (oversold)", medium)
	}
	if s.plantStatus(oldest[0]) != "held" || s.plantStatus(newer[0]) != "for_sale" {
		t.Errorf("statuses %s and %s, want the oldest held and the newer one still for sale", s.plantStatus(oldest[0]), s.plantStatus(newer[0]))
	}

	// Held plants still count as on hand at Shopify: Shopify subtracts the order as committed.
	s.chooseLocation(greenhouse)
	if err := s.syncStock(product); err != nil {
		t.Fatal(err)
	}
	if s.onHand("Small Cup", greenhouse) != 3 {
		t.Errorf("Small Cup on hand %d, want 3 (2 held + 1 for sale)", s.onHand("Small Cup", greenhouse))
	}

	// Their history names the order; staff can't repot or discard them by barcode meanwhile.
	var details string
	err := s.db.QueryRow(t.Context(), `
		SELECT e.details::text FROM plant_events e JOIN plants p ON p.id = e.plant_id
		WHERE p.barcode = $1 AND e.type = 'held_for_order'`, oldest[0]).Scan(&details)
	if err != nil || details != `{"order": "#1001"}` {
		t.Errorf("history: %s, %v; want held for #1001", details, err)
	}

	_, err = inventory.NewStore(s.db).DiscardPlants(t.Context(), s.org.ID, []string{oldest[0]}, nil)
	var barcodeErr *inventory.BarcodeError
	if !errors.As(err, &barcodeErr) || len(barcodeErr.Held) != 1 || s.plantStatus(oldest[0]) != "held" {
		t.Errorf("discarding a held plant: %v, want refused as held for an order", err)
	}

	// Importing again (overlapping imports, repeated webhooks) holds nothing more.
	s.importOrders()
	if orders := s.ordersWith("open"); len(orders) != 1 || len(orders[0].Items[0].HeldBarcodes) != 2 || s.plantStatus(newer[0]) != "for_sale" {
		t.Errorf("after importing again: %+v, want the same order and holds", orders)
	}
}

func TestOrderCancelledThenFulfilled(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	product, _ := s.addProduct(0, "Ordered", map[string]*int{"Small Cup": cents(1500)})
	plants := s.addPlants(product, "Small Cup", "for_sale", 2)
	s.sync(product)

	cancelled := s.placeOrder("#1001", map[string]int{"Small Cup": 1})
	pickedUp := s.placeOrder("#1002", map[string]int{"Small Cup": 1})
	s.importOrders()
	if s.plantStatus(plants[0]) != "held" || s.plantStatus(plants[1]) != "held" {
		t.Fatal("want both plants held")
	}

	// The customer cancels #1001: its plant goes back on sale.
	cancelled["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
	// #1002 is handed over without scanning, and marked fulfilled in Shopify: its plant is sold.
	pickedUp["displayFulfillmentStatus"] = "FULFILLED"
	s.importOrders()

	if s.plantStatus(plants[0]) != "for_sale" || s.plantStatus(plants[1]) != "sold" {
		t.Errorf("statuses %s and %s, want back on sale and sold", s.plantStatus(plants[0]), s.plantStatus(plants[1]))
	}
	if open := s.ordersWith("open"); len(open) != 0 {
		t.Errorf("open orders %+v, want none", open)
	}
	if c, p := s.ordersWith("cancelled"), s.ordersWith("picked_up"); len(c) != 1 || c[0].Name != "#1001" || len(p) != 1 || p[0].Name != "#1002" {
		t.Errorf("cancelled %+v, picked up %+v; want #1001 and #1002", c, p)
	}

	// An order that's over before Plant Stock sees it holds nothing.
	s.importOrdersFromScratch()
	gone := s.placeOrder("#1003", map[string]int{"Small Cup": 1})
	gone["cancelledAt"] = time.Now().UTC().Format(time.RFC3339)
	s.importOrders()
	if s.plantStatus(plants[0]) != "for_sale" || len(s.ordersWith("cancelled")) != 1 {
		t.Error("a cancelled order Plant Stock never saw held a plant")
	}
}

// importOrdersFromScratch forgets the orders read so far on the fake store (they're in Plant Stock).
func (s *shopTest) importOrdersFromScratch() { s.fake.orders = nil }

func TestOrderItemsNotInPlantStock(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	s.fake.orders = []map[string]any{{
		"id": "gid://shopify/Order/9", "name": "#1009", "createdAt": time.Now().UTC().Format(time.RFC3339),
		"updatedAt": time.Now().UTC().Format(time.RFC3339), "cancelledAt": nil, "displayFulfillmentStatus": "UNFULFILLED",
		"lineItems": map[string]any{"nodes": []map[string]any{{
			"id": "gid://shopify/LineItem/1", "title": "Gift card", "sku": nil, "quantity": 1, "variant": nil,
			"originalUnitPriceSet": map[string]any{"shopMoney": map[string]string{"amount": "25.5"}},
		}}},
	}}
	s.importOrders()
	orders := s.ordersWith("open")
	if len(orders) != 1 || orders[0].Items[0].InPlantStock || *orders[0].Items[0].PriceCents != 2550 {
		t.Errorf("orders = %+v, want the gift card listed, not Plant Stock's, $25.50", orders)
	}
}

func TestOrderImportSearchesAndRecordsProblems(t *testing.T) {
	s := newShopTest(t)
	s.connect()

	// The first import looks for open, unfulfilled orders; later ones for any changed since.
	s.importOrders()
	if !strings.Contains(s.fake.lastSearch, "status:open") || !strings.Contains(s.fake.lastSearch, "fulfillment_status:unfulfilled") {
		t.Errorf("first search %q, want open unfulfilled orders", s.fake.lastSearch)
	}
	s.importOrders()
	if !strings.HasPrefix(s.fake.lastSearch, "updated_at:>=") {
		t.Errorf("next search %q, want orders updated since the last import", s.fake.lastSearch)
	}

	// Shopify won't let the app read orders yet: recorded for Settings (and the Orders page), not retried.
	s.fake.ordersError = "This app is not approved to access the Order object."
	s.importOrders()
	var status struct {
		Connected bool
		Error     *string
	}
	_ = json.Unmarshal(s.expect("admin", "GET", "/api/orgs/{org}/shopify/orders/status", "", 200).Body.Bytes(), &status)
	if status.Error == nil || !strings.Contains(*status.Error, "not approved") {
		t.Errorf("orders status = %+v, want the problem shown", status)
	}
	s.fake.ordersError = ""
	s.importOrders()
	_ = json.Unmarshal(s.expect("admin", "GET", "/api/orgs/{org}/shopify/orders/status", "", 200).Body.Bytes(), &status)
	if status.Error != nil {
		t.Errorf("after it works again: %+v, want the problem cleared", status)
	}

	s.expect("staff", "POST", "/api/orgs/{org}/shopify/orders/import", "", 202)
	s.expect("viewer", "POST", "/api/orgs/{org}/shopify/orders/import", "", 403)
}

func (s *shopTest) webhook(topic, shop, body string, sign bool) int {
	s.t.Helper()
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(body))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !sign {
		signature = base64.StdEncoding.EncodeToString([]byte("forged"))
	}
	request := httptest.NewRequest("POST", "/api/shopify/webhooks", bytes.NewBufferString(body))
	request.Header.Set("X-Shopify-Hmac-Sha256", signature)
	request.Header.Set("X-Shopify-Topic", topic)
	request.Header.Set("X-Shopify-Shop-Domain", shop)
	recorder := httptest.NewRecorder()
	s.router("").ServeHTTP(recorder, request)
	return recorder.Code
}

func TestWebhooks(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	if _, err := s.db.Exec(t.Context(), "DELETE FROM jobs"); err != nil {
		t.Fatal(err)
	}

	if code := s.webhook("orders/create", testShop, `{"id":1}`, false); code != 401 {
		t.Errorf("forged webhook: %d, want 401", code)
	}
	if code := s.webhook("orders/create", testShop, `{"id":1}`, true); code != 200 || s.waitingJobs(JobImportOrders, 0) != 1 {
		t.Errorf("order webhook: %d, %d imports queued; want 200 and 1", code, s.waitingJobs(JobImportOrders, 0))
	}
	if code := s.webhook("orders/create", "unknown.myshopify.com", `{}`, true); code != 200 {
		t.Errorf("webhook from a store no one connected: %d, want 200 (nothing to do)", code)
	}
	if code := s.webhook("customers/redact", testShop, `{"shop_domain": "`+testShop+`"}`, true); code != 200 {
		t.Errorf("privacy webhook with no orders: %d, want 200 (nothing to do)", code)
	}
	if code := s.webhook("app/uninstalled", testShop, `{"myshopify_domain": "`+testShop+`"}`, true); code != 200 || !s.connection("owner").NeedsReconnect {
		t.Errorf("uninstall webhook: %d; want 200 and the store marked to connect again", code)
	}
}
