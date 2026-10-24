package shopify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nervster/root_access_inventory_management/backend/internal/pickup"
)

// proxyGet sends a GET through the App Proxy for shop, with the parameters Shopify adds, signed with
// secret as Shopify would (written out here rather than with VerifyProxy, so the test checks it).
func (s *shopTest) proxyGet(path, shop, secret string, extra url.Values) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.proxyRequest("GET", path, shop, secret, extra, "")
}

// proxyRequest is proxyGet for any method, with a JSON body (Shopify forwards the body; only the query
// is signed).
func (s *shopTest) proxyRequest(method, path, shop, secret string, extra url.Values, body string) *httptest.ResponseRecorder {
	s.t.Helper()
	query := url.Values{
		"shop": {shop}, "logged_in_customer_id": {""}, "path_prefix": {"/apps/plant-stock"},
		"timestamp": {strconv.FormatInt(time.Now().Unix(), 10)},
	}
	for k, v := range extra {
		query[k] = v
	}
	// Shopify's recipe: "key=value" pairs (values joined by commas), sorted, concatenated.
	message := ""
	for _, pair := range sortedPairs(query) {
		message += pair
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	query.Set("signature", hex.EncodeToString(mac.Sum(nil)))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/shopify/proxy"+path+"?"+query.Encode(), strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.2")
	s.router("").ServeHTTP(recorder, request)
	return recorder
}

func sortedPairs(query url.Values) []string {
	keys := []string{}
	for k := range query {
		keys = append(keys, k)
	}
	pairs := []string{}
	for _, k := range keys {
		v := ""
		for i, value := range query[k] {
			if i > 0 {
				v += ","
			}
			v += value
		}
		pairs = append(pairs, k+"="+v)
	}
	// insertion sort keeps the test free of the code under test
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j] < pairs[j-1]; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	return pairs
}

func TestProxyPickupSlots(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	slots := pickup.NewStore(s.db)
	if err := slots.SetUpWindows(t.Context(), s.org.ID, 0, 8, time.Now()); err != nil { // 12 AM, 8 AM, 4 PM
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/Chicago")
	today := time.Now().In(loc)
	closedDay := pickup.Date(today.Year(), today.Month(), today.Day()+2)
	if err := slots.Close(t.Context(), s.org.ID, closedDay, nil, nil); err != nil {
		t.Fatal(err)
	}

	recorder := s.proxyGet("/pickup-slots", testShop, "test-secret", url.Values{"extra": {"1", "2"}})
	if recorder.Code != 200 || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("signed request: %d (%s), want 200 and no-store", recorder.Code, recorder.Body)
	}
	var got pickupSlotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.TimeZone != "America/Chicago" || got.LeadHours != 8 || len(got.Days) != 30 || got.Days[0].Date != today.Format(time.DateOnly) {
		t.Fatalf("response = %s days from %s (%s, %d h), want 30 days from today in America/Chicago", got.TimeZone, got.Days[0].Date, got.TimeZone, got.LeadHours)
	}
	// Every date is listed; a closed one has no slots, an ordinary one all three.
	if day := got.Days[2]; day.Date != closedDay.Format(time.DateOnly) || len(day.Slots) != 0 {
		t.Errorf("closed day = %+v, want %s with no slots", day, closedDay.Format(time.DateOnly))
	}
	day := got.Days[3]
	if len(day.Slots) != 3 || day.Slots[1].Window != "8 AM – 4 PM" || day.Slots[1].Start.Hour() != 8 || day.Slots[1].End.Sub(day.Slots[1].Start) != 8*time.Hour {
		t.Errorf("open day = %+v, want three windows, the second 8 AM – 4 PM", day)
	}
	if _, offset := day.Slots[0].Start.Zone(); offset != -5*3600 && offset != -6*3600 {
		t.Errorf("start %s, want Houston's offset", day.Slots[0].Start)
	}

	// Anything Shopify didn't sign, or from a store no nursery connected, is refused.
	if code := s.proxyGet("/pickup-slots", testShop, "wrong-secret", nil).Code; code != 401 {
		t.Errorf("wrong signature: %d, want 401", code)
	}
	if code := s.proxyGet("/pickup-slots", "unknown.myshopify.com", "test-secret", nil).Code; code != 404 {
		t.Errorf("store no one connected: %d, want 404", code)
	}
	if code := s.proxyGet("/pickup-slots", "evil.example.com", "test-secret", nil).Code; code != 401 {
		t.Errorf("not a myshopify.com store: %d, want 401", code)
	}
	recorder = httptest.NewRecorder()
	s.router("").ServeHTTP(recorder, httptest.NewRequest("GET", "/api/shopify/proxy/pickup-slots?shop="+testShop, nil))
	if recorder.Code != 401 {
		t.Errorf("unsigned: %d, want 401", recorder.Code)
	}
}

func TestProxyHolds(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	slots := pickup.NewStore(s.db)
	if err := slots.SetUpWindows(t.Context(), s.org.ID, 0, 8, time.Now()); err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/Chicago")
	today := time.Now().In(loc)
	date := pickup.Date(today.Year(), today.Month(), today.Day()+3).Format(time.DateOnly)
	rules, _ := slots.Rules(t.Context(), s.org.ID)
	morning := rules.Windows[1]
	post := func(path, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		recorder := s.proxyRequest("POST", path, testShop, "test-secret", nil, body)
		if recorder.Code != want {
			t.Fatalf("POST %s %s: %d (%s), want %d", path, body, recorder.Code, recorder.Body, want)
		}
		return recorder
	}
	slotListed := func(cart string) bool {
		t.Helper()
		var got pickupSlotsResponse
		_ = json.Unmarshal(s.proxyGet("/pickup-slots", testShop, "test-secret", url.Values{"cartToken": {cart}}).Body.Bytes(), &got)
		for _, d := range got.Days {
			for _, slot := range d.Slots {
				if d.Date == date && slot.WindowID == morning.ID {
					return true
				}
			}
		}
		return false
	}

	// Hold: 201 with the hold, in Houston time; the slot leaves other carts' lists but stays in its own.
	var hold holdResponse
	_ = json.Unmarshal(post("/pickup-holds", fmt.Sprintf(`{"cartToken":"c1-a","date":%q,"windowId":%d}`, date, morning.ID), 201).Body.Bytes(), &hold)
	if hold.HoldID == "" || hold.Date != date || hold.Window != morning.Name || hold.Start.Hour() != 8 || time.Until(hold.ExpiresAt) < 29*time.Minute {
		t.Errorf("hold = %+v, want the slot held for 30 minutes", hold)
	}
	if slotListed("c1-b") || !slotListed("c1-a") {
		t.Errorf("listed for another cart: %v, for its own: %v; want hidden, then listed", slotListed("c1-b"), slotListed("c1-a"))
	}

	// Another cart: 409 taken, with a code and a sentence.
	var failure struct{ Error, Message string }
	_ = json.Unmarshal(post("/pickup-holds", fmt.Sprintf(`{"cartToken":"c1-b","date":%q,"windowId":%d}`, date, morning.ID), 409).Body.Bytes(), &failure)
	if failure.Error != "taken" || failure.Message == "" {
		t.Errorf("failure = %+v, want taken with a message", failure)
	}

	// Refresh, release; then the slot is back for everyone.
	post("/pickup-holds/refresh", fmt.Sprintf(`{"cartToken":"c1-a","holdId":%q}`, hold.HoldID), 200)
	post("/pickup-holds/release", fmt.Sprintf(`{"cartToken":"c1-a","holdId":%q}`, hold.HoldID), 204)
	if !slotListed("c1-b") {
		t.Error("after release, want the slot listed again")
	}
	_ = json.Unmarshal(post("/pickup-holds/refresh", fmt.Sprintf(`{"cartToken":"c1-a","holdId":%q}`, hold.HoldID), 404).Body.Bytes(), &failure)
	if failure.Error != "not_found" {
		t.Errorf("refresh after release = %+v, want not_found", failure)
	}

	// Bad requests: invalid. Unsigned: 401.
	_ = json.Unmarshal(post("/pickup-holds", `{"date":"2026-10-24","windowId":1}`, 400).Body.Bytes(), &failure)
	if failure.Error != "invalid" {
		t.Errorf("no cartToken = %+v, want invalid", failure)
	}
	post("/pickup-holds", `{"cartToken":"c1-a","date":"24 Oct","windowId":1}`, 400)
	post("/pickup-holds/refresh", `{"cartToken":"c1-a"}`, 400)
	if code := s.proxyRequest("POST", "/pickup-holds", testShop, "wrong-secret", nil, `{}`).Code; code != 401 {
		t.Errorf("wrong signature: %d, want 401", code)
	}
}
