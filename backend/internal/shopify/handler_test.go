package shopify

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbtest"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
)

const testShop = "root-access-test.myshopify.com"

// fakeShopify stands in for Shopify: it hands out tokens for the code "good-code", refreshes them,
// and answers the locations query for whoever holds the current access token.
type fakeShopify struct {
	mu            sync.Mutex
	scope         string // granted on connect
	accessToken   string
	refreshToken  string
	refreshes     int
	refuseRefresh bool
	issued        int
}

func (f *fakeShopify) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/admin/oauth/access_token":
		_ = r.ParseForm()
		if r.Form.Get("client_secret") != "test-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusBadRequest)
			return
		}
		switch {
		case r.Form.Get("code") == "good-code" && r.Form.Get("expiring") == "1":
		case r.Form.Get("grant_type") == "refresh_token" && !f.refuseRefresh && r.Form.Get("refresh_token") == f.refreshToken:
			f.refreshes++
		default:
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		f.issued++
		f.accessToken, f.refreshToken = fmt.Sprintf("shpat_%d", f.issued), fmt.Sprintf("shprt_%d", f.issued)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": f.accessToken, "expires_in": 3600, "scope": f.scope,
			"refresh_token": f.refreshToken, "refresh_token_expires_in": 7776000,
		})
	case "/admin/api/" + APIVersion + "/graphql.json":
		if r.Header.Get("X-Shopify-Access-Token") != f.accessToken {
			http.Error(w, `{"errors":"Invalid API key or access token"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"locations":{"nodes":[
			{"id":"gid://shopify/Location/1","name":"Greenhouse"},
			{"id":"gid://shopify/Location/2","name":"Market Stall"}]}}}`))
	default:
		http.NotFound(w, r)
	}
}

// shopTest is a nursery (Owner and Admin) and another nursery's Owner ("rival"), with a fake Shopify.
type shopTest struct {
	t       *testing.T
	db      db.Conn
	org     dbgen.Organization
	other   dbgen.Organization
	users   map[string]dbgen.User
	fake    *fakeShopify
	app     *App // signs callbacks as Shopify would
	store   *Store
	cookies map[string]string // state cookie per user, as their browser would keep it
}

func newShopTest(t *testing.T) *shopTest {
	t.Helper()
	tx := dbtest.Tx(t)
	q := dbgen.New(tx)
	ctx := t.Context()

	s := &shopTest{t: t, db: tx, users: map[string]dbgen.User{}, cookies: map[string]string{},
		fake: &fakeShopify{scope: strings.Join(Scopes, ",")}}
	for i, slug := range []string{"shopify-test", "shopify-rival"} {
		org, err := q.CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: slug, Slug: slug, TimeZone: "America/Chicago"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			s.org = org
		} else {
			s.other = org
		}
	}
	for name, m := range map[string]struct {
		org  int64
		role dbgen.OrgRole
	}{
		"owner": {s.org.ID, dbgen.OrgRoleOwner},
		"admin": {s.org.ID, dbgen.OrgRoleAdmin},
		"rival": {s.other.ID, dbgen.OrgRoleOwner},
	} {
		displayName := "Pat " + name
		user, err := q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: "user_shopify_" + name, Email: name + "@shopify.example.com", DisplayName: &displayName})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.CreateMembership(ctx, dbgen.CreateMembershipParams{OrganizationID: m.org, UserID: user.ID, Role: m.role}); err != nil {
			t.Fatal(err)
		}
		s.users[name] = user
	}

	server := httptest.NewServer(s.fake)
	t.Cleanup(server.Close)
	app := NewApp("test-client", "test-secret", "http://localhost:5173/api/shopify/callback")
	app.shopURL = func(string) string { return server.URL }
	sealer, err := NewSealer(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	s.app, s.store = app, NewStore(tx, app, sealer)
	return s
}

func (s *shopTest) router(as string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api")
	signedIn := router.Group("/api", func(c *gin.Context) {
		auth.SetCurrentUser(c, s.users[as], false)
		c.Next()
	})
	Routes(api, signedIn, organization.NewStore(s.db), s.store, "http://localhost:5173")
	return router
}

// expect sends a request as the named user to their nursery ({org} in path) and checks the status.
func (s *shopTest) expect(as, method, path, body string, wantStatus int) *httptest.ResponseRecorder {
	s.t.Helper()
	orgID := s.org.ID
	if as == "rival" {
		orgID = s.other.ID
	}
	path = strings.ReplaceAll(path, "{org}", fmt.Sprint(orgID))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	s.router(as).ServeHTTP(recorder, request)
	if recorder.Code != wantStatus {
		s.t.Fatalf("%s %s %s as %s: status = %d, want %d (body: %s)", method, path, body, as, recorder.Code, wantStatus, recorder.Body)
	}
	return recorder
}

// startConnect clicks Connect as the named user and returns the state from Shopify's approval link.
func (s *shopTest) startConnect(as, shop string) string {
	s.t.Helper()
	recorder := s.expect(as, "POST", "/api/orgs/{org}/shopify/connect", fmt.Sprintf(`{"shop":%q}`, shop), 200)
	var body struct{ AuthorizeURL string }
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	link, err := url.Parse(body.AuthorizeURL)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == stateCookie && cookie.HttpOnly && cookie.Path == callbackPath {
			s.cookies[as] = cookie.Value
		}
	}
	if s.cookies[as] != link.Query().Get("state") {
		s.t.Fatalf("cookie state %q, link state %q; want the same", s.cookies[as], link.Query().Get("state"))
	}
	return link.Query().Get("state")
}

// callback is Shopify sending the browser back. sign=false forges it; cookie is what the browser sends.
func (s *shopTest) callback(shop, state, cookie string, sign bool) *httptest.ResponseRecorder {
	s.t.Helper()
	query := url.Values{"code": {"good-code"}, "shop": {shop}, "state": {state}, "timestamp": {"1760000000"}, "host": {"YWRtaW4"}}
	signature := s.app.sign(query)
	if !sign {
		signature[0] ^= 1
	}
	query.Set("hmac", hex.EncodeToString(signature))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/shopify/callback?"+query.Encode(), nil)
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: stateCookie, Value: cookie})
	}
	s.router("").ServeHTTP(recorder, request)
	return recorder
}

// wantResult checks the callback sent the browser to the nursery's Settings with this result.
func (s *shopTest) wantResult(recorder *httptest.ResponseRecorder, orgID int64, result string) {
	s.t.Helper()
	want := fmt.Sprintf("http://localhost:5173/orgs/%d/settings?shopify=%s", orgID, result)
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != want {
		s.t.Fatalf("callback: %d to %q (body %s), want a redirect to %s", recorder.Code, recorder.Header().Get("Location"), recorder.Body, want)
	}
}

func (s *shopTest) connection(as string) connectionResponse {
	s.t.Helper()
	var connection connectionResponse
	_ = json.Unmarshal(s.expect(as, "GET", "/api/orgs/{org}/shopify", "", 200).Body.Bytes(), &connection)
	return connection
}

func (s *shopTest) connect() {
	s.t.Helper()
	state := s.startConnect("owner", testShop)
	s.wantResult(s.callback(testShop, state, s.cookies["owner"], true), s.org.ID, "connected")
}

func TestConnectAStore(t *testing.T) {
	s := newShopTest(t)
	if got := s.connection("owner"); !got.Configured || got.Connected {
		t.Fatalf("before = %+v, want configured and not connected", got)
	}

	// The Owner types the store's name any which way; the approval link asks for Plant Stock's scopes.
	recorder := s.expect("owner", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":" https://Root-Access-Test.myshopify.com/admin "}`, 200)
	var body struct{ AuthorizeURL string }
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	link, _ := url.Parse(body.AuthorizeURL)
	if link.Path != "/admin/oauth/authorize" || link.Query().Get("client_id") != "test-client" ||
		link.Query().Get("scope") != strings.Join(Scopes, ",") || link.Query().Get("redirect_uri") != "http://localhost:5173/api/shopify/callback" {
		t.Errorf("approval link = %s", body.AuthorizeURL)
	}

	s.connect()
	got := s.connection("owner")
	if !got.Connected || *got.Shop != testShop || got.NeedsReconnect || got.LocationID != nil || *got.ConnectedByName != "Pat owner" {
		t.Errorf("after = %+v, want connected to %s by Pat owner, no location yet", got, testShop)
	}

	// The tokens are stored encrypted.
	var accessToken []byte
	if err := s.db.QueryRow(t.Context(), "SELECT access_token FROM shopify_connections WHERE organization_id = $1", s.org.ID).Scan(&accessToken); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(accessToken, []byte("shpat_")) {
		t.Error("the access token is stored readable")
	}

	// Choosing the pickup location.
	var locations []Location
	_ = json.Unmarshal(s.expect("owner", "GET", "/api/orgs/{org}/shopify/locations", "", 200).Body.Bytes(), &locations)
	if len(locations) != 2 || locations[0].Name != "Greenhouse" {
		t.Fatalf("locations = %+v, want the store's two", locations)
	}
	s.expect("owner", "PUT", "/api/orgs/{org}/shopify/location", `{"locationId":"gid://shopify/Location/99"}`, 400)
	var withLocation connectionResponse
	_ = json.Unmarshal(s.expect("owner", "PUT", "/api/orgs/{org}/shopify/location", `{"locationId":"gid://shopify/Location/1"}`, 200).Body.Bytes(), &withLocation)
	if withLocation.LocationName == nil || *withLocation.LocationName != "Greenhouse" {
		t.Errorf("location = %v, want Greenhouse", withLocation.LocationName)
	}

	// Connecting the same store again keeps the location; a different one needs a disconnect first.
	s.connect()
	if got := s.connection("owner"); got.LocationName == nil || *got.LocationName != "Greenhouse" {
		t.Errorf("after reconnecting, location = %v, want Greenhouse kept", got.LocationName)
	}
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":"another-store"}`, 409)

	s.expect("owner", "DELETE", "/api/orgs/{org}/shopify", "", 204)
	if got := s.connection("owner"); got.Connected {
		t.Errorf("after disconnecting = %+v", got)
	}
	s.expect("owner", "DELETE", "/api/orgs/{org}/shopify", "", 404)
}

func TestOnlyOwnersConnect(t *testing.T) {
	s := newShopTest(t)
	s.expect("admin", "GET", "/api/orgs/{org}/shopify", "", 403)
	s.expect("admin", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":"root-access-test"}`, 403)
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":"not a store!"}`, 400)
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":"evil.com"}`, 400)

	// An Owner who stops being one before Shopify sends them back isn't connected.
	state := s.startConnect("owner", testShop)
	if _, err := s.db.Exec(t.Context(), "UPDATE memberships SET role = 'staff' WHERE user_id = $1", s.users["owner"].ID); err != nil {
		t.Fatal(err)
	}
	s.wantResult(s.callback(testShop, state, s.cookies["owner"], true), s.org.ID, "not-allowed")
	if _, err := s.store.Connection(t.Context(), s.org.ID); err != ErrNotConnected {
		t.Errorf("connection error = %v, want not connected", err)
	}
}

func TestCallbackChecks(t *testing.T) {
	s := newShopTest(t)
	state := s.startConnect("owner", testShop)
	cookie := s.cookies["owner"]

	if got := s.callback(testShop, state, cookie, false); got.Code != 400 {
		t.Errorf("forged signature: status %d, want 400", got.Code)
	}
	if got := s.callback(testShop, state, "", true); got.Code != 400 {
		t.Errorf("another browser (no cookie): status %d, want 400", got.Code)
	}
	if got := s.callback(testShop, "made-up", "made-up", true); got.Code != 400 {
		t.Errorf("unknown state: status %d, want 400", got.Code)
	}
	// The Owner typed one store, then approved on a different one.
	s.wantResult(s.callback("someone-else.myshopify.com", state, cookie, true), s.org.ID, "different-store")
	if got := s.callback(testShop, state, cookie, true); got.Code != 400 {
		t.Errorf("reusing the state: status %d, want 400", got.Code)
	}

	// Too slow on Shopify's page.
	state = s.startConnect("owner", testShop)
	if _, err := s.db.Exec(t.Context(), "UPDATE shopify_oauth_states SET created_at = now() - interval '11 minutes'"); err != nil {
		t.Fatal(err)
	}
	s.wantResult(s.callback(testShop, state, s.cookies["owner"], true), s.org.ID, "expired")

	// The store didn't grant every scope.
	s.fake.scope = "write_products,read_locations"
	state = s.startConnect("owner", testShop)
	s.wantResult(s.callback(testShop, state, s.cookies["owner"], true), s.org.ID, "missing-permissions")
	s.fake.scope = strings.Join(Scopes, ",")

	// A store belongs to one nursery.
	s.connect()
	state = s.startConnect("rival", testShop)
	s.wantResult(s.callback(testShop, state, s.cookies["rival"], true), s.other.ID, "other-nursery")
	if got := s.connection("rival"); got.Connected {
		t.Errorf("rival = %+v, want not connected", got)
	}
}

func TestTokenRefresh(t *testing.T) {
	s := newShopTest(t)
	s.connect()
	expire := func() {
		if _, err := s.db.Exec(t.Context(), "UPDATE shopify_connections SET access_token_expires_at = now() + interval '30 seconds'"); err != nil {
			t.Fatal(err)
		}
	}

	// A token about to expire is refreshed once; the next call uses the new one.
	expire()
	s.expect("owner", "GET", "/api/orgs/{org}/shopify/locations", "", 200)
	s.expect("owner", "GET", "/api/orgs/{org}/shopify/locations", "", 200)
	if s.fake.refreshes != 1 {
		t.Errorf("refreshes = %d, want 1", s.fake.refreshes)
	}

	// Shopify refuses the refresh token (e.g. the app was uninstalled): connect again.
	s.fake.refuseRefresh = true
	expire()
	s.expect("owner", "GET", "/api/orgs/{org}/shopify/locations", "", 409)
	if got := s.connection("owner"); !got.NeedsReconnect {
		t.Errorf("connection = %+v, want needs reconnect", got)
	}

	// Connecting again fixes it.
	s.fake.refuseRefresh = false
	s.connect()
	if got := s.connection("owner"); got.NeedsReconnect {
		t.Errorf("after reconnecting = %+v", got)
	}
	s.expect("owner", "GET", "/api/orgs/{org}/shopify/locations", "", 200)
}

func TestNotConfigured(t *testing.T) {
	s := newShopTest(t)
	s.store = NewStore(s.db, nil, nil)
	if got := s.connection("owner"); got.Configured {
		t.Errorf("connection = %+v, want not configured", got)
	}
	s.expect("owner", "POST", "/api/orgs/{org}/shopify/connect", `{"shop":"root-access-test"}`, 503)
	if got := s.callback(testShop, "x", "x", false); got.Code != 400 {
		t.Errorf("callback: status %d, want 400", got.Code)
	}
}
