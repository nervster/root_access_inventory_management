package shopify

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

type returnJSON struct {
	ID            int64
	Name          string
	OrderName     string
	ShopifyStatus string
	ReceivedAt    *time.Time
	RefundDueAt   *time.Time
	RefundOverdue bool
	Refunded      bool
	Items         []struct {
		Title           string
		Quantity        int
		Reason          *string
		CustomerNote    *string
		SoldBarcodes    []string
		WaitingBarcodes []string
		ReceivedCount   int
	}
}

func (s *shopTest) returnsIn(as, which string) []returnJSON {
	s.t.Helper()
	var list []returnJSON
	_ = json.Unmarshal(s.ordersRequest(as, "GET", "/returns?show="+which, 200).Body.Bytes(), &list)
	return list
}

// pickedUpOrder sells qty plants to an order, picked up and fulfilled, and returns the Shopify order.
func (s *shopTest) pickedUpOrder(qty int) (shopifyOrder map[string]any, plants []string) {
	s.t.Helper()
	s.connect()
	product, _ := s.addProduct(0, "Returned", map[string]*int{"Small Cup": cents(1500)})
	plants = s.addPlants(product, "Small Cup", "for_sale", qty)
	s.sync(product)
	shopifyOrder = s.placeOrder("#1001", map[string]int{"Small Cup": qty})
	s.importOrders()
	order := s.ordersIn("viewer", "open")[0]
	s.ordersRequest("staff", "POST", fmt.Sprintf("/orders/%d/picked-up", order.ID), 204)
	if err := s.store.FulfillOrder(s.t.Context(), s.org.ID, order.ID); err != nil {
		s.t.Fatal(err)
	}
	return shopifyOrder, plants
}

// requestReturn puts a return request on the store for qty of the order's first item.
func (s *shopTest) requestReturn(order map[string]any, qty int, refunded int) map[string]any {
	s.t.Helper()
	line := order["lineItems"].(map[string]any)["nodes"].([]map[string]any)[0]
	ret := map[string]any{
		"id": s.fake.id("Return"), "name": order["name"].(string) + "-R1", "status": "REQUESTED",
		"createdAt": time.Now().UTC().Format(time.RFC3339),
		"returnLineItems": map[string]any{"nodes": []map[string]any{{
			"id": s.fake.id("ReturnLineItem"), "quantity": qty, "refundedQuantity": refunded,
			"customerNote": "Leaves arrived yellow", "returnReasonDefinition": map[string]string{"name": "Damaged"},
			"fulfillmentLineItem": map[string]any{"lineItem": map[string]string{"id": line["id"].(string)}},
		}}},
	}
	if s.fake.returns == nil {
		s.fake.returns = map[string][]map[string]any{}
	}
	s.fake.returns[order["id"].(string)] = append(s.fake.returns[order["id"].(string)], ret)
	order["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	return ret
}

func TestReturn(t *testing.T) {
	s := newShopTest(t)
	order, plants := s.pickedUpOrder(2)
	ret := s.requestReturn(order, 1, 0)
	s.importOrders()

	// The request is in the queue, with the reason and note, and the order's sold plants to pick from.
	queue := s.returnsIn("viewer", "active")
	if len(queue) != 1 || queue[0].Name != "#1001-R1" || queue[0].OrderName != "#1001" || queue[0].ReceivedAt != nil {
		t.Fatalf("queue = %+v, want #1001-R1 waiting for the plant", queue)
	}
	item := queue[0].Items[0]
	if item.Quantity != 1 || *item.Reason != "Damaged" || *item.CustomerNote != "Leaves arrived yellow" || len(item.SoldBarcodes) != 2 {
		t.Errorf("item = %+v, want 1 coming back (Damaged), both sold plants to pick from", item)
	}
	path := fmt.Sprintf("/returns/%d", queue[0].ID)

	// The plant comes back: staff say which one. Only a sold plant from this order, and only as many as are returned.
	s.ordersRequest("viewer", "POST", path+"/receive", 403)
	s.receive(path, "IT-NOPE", 400)
	s.receive(path, plants[0], 204)
	s.receive(path, plants[1], 400) // only 1 is coming back
	if s.plantStatus(plants[0]) != "returned" || s.plantStatus(plants[1]) != "sold" {
		t.Fatalf("statuses %s and %s, want returned and still sold", s.plantStatus(plants[0]), s.plantStatus(plants[1]))
	}
	queue = s.returnsIn("viewer", "active")
	if queue[0].ReceivedAt == nil || queue[0].RefundDueAt == nil || queue[0].RefundDueAt.Sub(*queue[0].ReceivedAt) != 24*time.Hour || queue[0].RefundOverdue ||
		fmt.Sprint(queue[0].Items[0].WaitingBarcodes) != fmt.Sprint([]string{plants[0]}) {
		t.Errorf("after receiving: %+v, want received, refund due a day later, and the plant waiting for its check", queue[0])
	}

	// Its history names the return.
	var details string
	err := s.db.QueryRow(t.Context(), `SELECT e.details::text FROM plant_events e JOIN plants p ON p.id = e.plant_id
		WHERE p.barcode = $1 AND e.type = 'returned'`, plants[0]).Scan(&details)
	if err != nil || details != `{"return": "#1001-R1"}` {
		t.Errorf("history: %s, %v; want returned with #1001-R1", details, err)
	}

	// Checked: back on sale (Shopify gets it back in stock).
	s.check(plants[0], `{"to":"sold"}`, 400)
	s.check(plants[0], `{"to":"for_sale"}`, 204)
	s.check(plants[0], `{"to":"for_sale"}`, 404) // not waiting for a check any more
	if s.plantStatus(plants[0]) != "for_sale" {
		t.Errorf("plant %s, want back on sale", s.plantStatus(plants[0]))
	}

	// Refunded in Shopify (without the order itself showing as changed): the return is done, and its
	// refund, tied to the return, isn't flagged.
	s.refund(order, ret, 1, "15.00")
	order["updatedAt"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	ret["status"] = "CLOSED"
	ret["returnLineItems"].(map[string]any)["nodes"].([]map[string]any)[0]["refundedQuantity"] = 1
	s.importOrders() // even without the order marked changed: unsettled returns are read again
	if active, done := s.returnsIn("viewer", "active"), s.returnsIn("viewer", "done"); len(active) != 0 || len(done) != 1 || !done[0].Refunded {
		t.Errorf("active %+v, done %+v; want the return done and refunded", active, done)
	}
	if flagged := s.unreturnedRefunds(); len(flagged) != 0 {
		t.Errorf("flagged %+v, want none: the refund was for the return", flagged)
	}
}

func TestReturnedPlantDiscardedOrRegrown(t *testing.T) {
	s := newShopTest(t)
	order, plants := s.pickedUpOrder(2)
	s.requestReturn(order, 2, 0)
	s.importOrders()
	path := fmt.Sprintf("/returns/%d", s.returnsIn("viewer", "active")[0].ID)
	s.receive(path, plants[0], 204)
	s.receive(path, plants[1], 204)

	s.check(plants[0], fmt.Sprintf(`{"to":"discarded","reason":%q}`, strings.Repeat("x", 501)), 400)
	s.check(plants[0], `{"to":"discarded","reason":" Root rot "}`, 204)
	s.check(plants[1], `{"to":"growing"}`, 204)
	var reason string
	if err := s.db.QueryRow(t.Context(), "SELECT discard_reason FROM plants WHERE barcode = $1", plants[0]).Scan(&reason); err != nil || reason != "Root rot" {
		t.Errorf("discard reason %q, %v; want Root rot", reason, err)
	}
	if s.plantStatus(plants[0]) != "discarded" || s.plantStatus(plants[1]) != "growing" {
		t.Errorf("statuses %s and %s, want discarded and growing", s.plantStatus(plants[0]), s.plantStatus(plants[1]))
	}
}

func TestReturnsNeedTheScope(t *testing.T) {
	s := newShopTest(t)
	order, _ := s.pickedUpOrder(1)
	// Connected before returns existed: the store never granted read_returns.
	_, err := s.db.Exec(t.Context(), "UPDATE shopify_connections SET scopes = replace(scopes, ',read_returns', '')")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.connection("owner"); fmt.Sprint(got.MissingScopes) != "[read_returns]" {
		t.Errorf("missing scopes %v, want [read_returns] so Settings asks to connect again", got.MissingScopes)
	}
	s.requestReturn(order, 1, 0)
	s.importOrders() // orders still import; returns wait
	if len(s.returnsIn("viewer", "active")) != 0 {
		t.Error("imported returns without the scope")
	}
}

// refund adds a refund to the order on the store: for qty of its first item (0: money only), tied
// to ret (nil: no return).
func (s *shopTest) refund(order, ret map[string]any, qty int, amount string) {
	s.t.Helper()
	line := order["lineItems"].(map[string]any)["nodes"].([]map[string]any)[0]
	refund := map[string]any{
		"id": s.fake.id("Refund"), "createdAt": time.Now().UTC().Format(time.RFC3339), "return": nil,
		"totalRefundedSet": map[string]any{"shopMoney": map[string]string{"amount": amount}},
		"refundLineItems":  map[string]any{"nodes": []map[string]any{}},
	}
	if ret != nil {
		refund["return"] = map[string]string{"id": ret["id"].(string)}
	}
	if qty > 0 {
		refund["refundLineItems"] = map[string]any{"nodes": []map[string]any{{"quantity": qty, "lineItem": map[string]string{"title": line["title"].(string)}}}}
	}
	if s.fake.refunds == nil {
		s.fake.refunds = map[string][]map[string]any{}
	}
	s.fake.refunds[order["id"].(string)] = append(s.fake.refunds[order["id"].(string)], refund)
}

type refundJSON struct {
	ID          int64
	OrderName   string
	AmountCents *int
	Items       *string
}

func (s *shopTest) unreturnedRefunds() []refundJSON {
	s.t.Helper()
	var list []refundJSON
	_ = json.Unmarshal(s.ordersRequest("viewer", "GET", "/refunds/unreturned", 200).Body.Bytes(), &list)
	return list
}

func TestRefundWithoutReturnIsFlagged(t *testing.T) {
	s := newShopTest(t)
	order, plants := s.pickedUpOrder(1)

	// Refund clicked instead of Return: flagged, with what was refunded.
	s.refund(order, nil, 1, "15.00")
	s.refund(order, nil, 0, "2.50") // a money-only refund breaks the rule too
	order["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	s.importOrders()
	flagged := s.unreturnedRefunds()
	if len(flagged) != 2 {
		t.Fatalf("flagged %+v, want both refunds", flagged)
	}
	var items, moneyOnly *refundJSON
	for i := range flagged {
		if flagged[i].Items != nil {
			items = &flagged[i]
		} else {
			moneyOnly = &flagged[i]
		}
	}
	if items == nil || moneyOnly == nil || *items.Items != "1 × Returned - Small Cup" || *items.AmountCents != 1500 || *moneyOnly.AmountCents != 250 || items.OrderName != "#1001" {
		t.Errorf("flagged %+v, want the item refund (1 × …, $15) and the money-only one ($2.50) on #1001", flagged)
	}
	if s.plantStatus(plants[0]) != "sold" {
		t.Error("a refund without a return changed the plant; it should stay sold until it's back")
	}

	// Approved as a special case, by an Owner, saying why: no longer flagged (even when Shopify sends
	// it again), and listed with the approvals.
	approve := func(as, body string, wantStatus int) {
		t.Helper()
		s.ordersRequestBody(as, "POST", fmt.Sprintf("/refunds/%d/approve", items.ID), body, wantStatus)
	}
	approve("admin", `{"note":"Arrived damaged"}`, 403) // only Owners
	approve("staff", `{"note":"Arrived damaged"}`, 403)
	approve("owner", `{"note":"   "}`, 400) // a note is required
	approve("owner", `{"note":" Arrived damaged, photo checked; customer kept it "}`, 204)
	approve("owner", `{"note":"again"}`, 404)
	order["updatedAt"] = time.Now().UTC().Format(time.RFC3339)
	s.importOrders()
	if flagged := s.unreturnedRefunds(); len(flagged) != 1 || flagged[0].ID != moneyOnly.ID {
		t.Errorf("after approving one: %+v, want only the money-only refund", flagged)
	}
	var approved []struct {
		ID             int64
		Note           string
		ApprovedByName *string
	}
	_ = json.Unmarshal(s.ordersRequest("viewer", "GET", "/refunds/approved", 200).Body.Bytes(), &approved)
	if len(approved) != 1 || approved[0].ID != items.ID || approved[0].Note != "Arrived damaged, photo checked; customer kept it" || *approved[0].ApprovedByName != "Pat owner" {
		t.Errorf("approved = %+v, want the refund with the note, by Pat owner", approved)
	}
}

func (s *shopTest) receive(returnPath, barcode string, wantStatus int) {
	s.t.Helper()
	s.ordersRequestBody("staff", "POST", returnPath+"/receive", fmt.Sprintf(`{"barcode":%q}`, barcode), wantStatus)
}

func (s *shopTest) check(barcode, body string, wantStatus int) {
	s.t.Helper()
	s.ordersRequestBody("staff", "POST", "/returned-plants/"+barcode+"/check", body, wantStatus)
}

func TestPrivacyRequestsClearReturnNotes(t *testing.T) {
	s := newShopTest(t)
	order, _ := s.pickedUpOrder(1)
	s.requestReturn(order, 1, 0)
	s.importOrders()
	note := func() *string {
		return s.returnsIn("viewer", "active")[0].Items[0].CustomerNote
	}
	orderID := strings.TrimPrefix(order["id"].(string), "gid://shopify/Order/")

	// customers/redact clears notes only on the orders it lists.
	if code := s.webhook("customers/redact", testShop, `{"shop_domain": "`+testShop+`", "orders_to_redact": [999999]}`, true); code != 200 || note() == nil {
		t.Errorf("redact for another order: %d, note %v; want 200 and the note kept", code, note())
	}
	if code := s.webhook("customers/redact", testShop, `{"shop_domain": "`+testShop+`", "orders_to_redact": [`+orderID+`]}`, true); code != 200 || note() != nil {
		t.Errorf("redact for the order: %d, note %v; want 200 and the note gone", code, note())
	}

	// shop/redact clears every note from the store, even once it's disconnected.
	s.importOrders() // the return isn't settled, so it's read again, note and all
	if note() == nil {
		t.Fatal("the note wasn't read again; this test needs another way to bring it back")
	}
	s.expect("owner", "DELETE", "/api/orgs/{org}/shopify", "", 204)
	if code := s.webhook("shop/redact", testShop, `{"shop_domain": "`+testShop+`"}`, true); code != 200 || note() != nil {
		t.Errorf("shop redact: %d, note %v; want 200 and the note gone", code, note())
	}
}
