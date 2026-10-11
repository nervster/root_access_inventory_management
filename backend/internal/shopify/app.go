// Package shopify links each nursery to its Shopify store and talks to Shopify's Admin API.
package shopify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// APIVersion is the Admin API version Plant Stock uses. Shopify releases one each quarter and keeps
// each for at least a year; update it (and test) at least once a year.
const APIVersion = "2026-10"

// Scopes are what Plant Stock asks each store for (the same list as the app's version in the Dev
// Dashboard). A write scope includes its read scope.
var Scopes = []string{
	"write_products",  // create and update products
	"write_inventory", // set stock
	"read_locations",  // choose the pickup location
	"read_orders",     // see what sold
	"write_merchant_managed_fulfillment_orders", // mark pickups as done
	"write_publications",                        // put products on the online store
}

var (
	// ErrReconnect means Shopify refused the store's tokens (e.g. the app was uninstalled, or the
	// refresh token expired): an Owner has to connect again.
	ErrReconnect = errors.New("shopify: the store must be connected again")
	// ErrUnavailable is a temporary problem at Shopify (rate limit or server error); try later.
	ErrUnavailable = errors.New("shopify: temporarily unavailable")
)

// App is Plant Stock's Shopify app: its credentials from the Dev Dashboard.
type App struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string // where Shopify sends the Owner back: <APP_URL>/api/shopify/callback
	HTTP         *http.Client

	shopURL func(shop string) string // https://<shop>; tests point it at a fake Shopify
}

func NewApp(clientID, clientSecret, redirectURL string) *App {
	return &App{
		ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL,
		HTTP: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *App) baseURL(shop string) string {
	if a.shopURL != nil {
		return a.shopURL(shop)
	}
	return "https://" + shop
}

var shopPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9\-]*\.myshopify\.com$`)

// NormalizeShop turns what an Owner types ("root-access-htx", "Root-Access-HTX.myshopify.com",
// "https://root-access-htx.myshopify.com/admin") into the store's address, or "" if it isn't one.
func NormalizeShop(input string) string {
	shop := strings.ToLower(strings.TrimSpace(input))
	shop = strings.TrimPrefix(strings.TrimPrefix(shop, "https://"), "http://")
	shop, _, _ = strings.Cut(shop, "/")
	if shop != "" && !strings.Contains(shop, ".") {
		shop += ".myshopify.com"
	}
	if !ValidShop(shop) {
		return ""
	}
	return shop
}

// ValidShop reports whether shop is a store address (something.myshopify.com). Checked before
// sending anything to it, so a forged callback can't make the API call some other server.
func ValidShop(shop string) bool {
	return shopPattern.MatchString(shop)
}

// AuthorizeURL is Shopify's page where the Owner approves Plant Stock for their store.
func (a *App) AuthorizeURL(shop, state string) string {
	query := url.Values{
		"client_id":    {a.ClientID},
		"scope":        {strings.Join(Scopes, ",")},
		"redirect_uri": {a.RedirectURL},
		"state":        {state},
	}
	return a.baseURL(shop) + "/admin/oauth/authorize?" + query.Encode()
}

// VerifyCallback reports whether Shopify signed the callback's query string with the app's secret
// (an HMAC-SHA256 of the other parameters, sorted), i.e. it really came from Shopify.
func (a *App) VerifyCallback(query url.Values) bool {
	given, err := hex.DecodeString(query.Get("hmac"))
	if err != nil || len(given) == 0 {
		return false
	}
	return hmac.Equal(given, a.sign(query)) // constant time, so the signature can't be guessed bit by bit
}

// VerifyWebhook reports whether Shopify signed a webhook's body with the app's secret (the
// X-Shopify-Hmac-Sha256 header: a base64 HMAC-SHA256 of the raw body).
func (a *App) VerifyWebhook(body []byte, signature string) bool {
	given, err := base64.StdEncoding.DecodeString(signature)
	if err != nil || len(given) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(a.ClientSecret))
	mac.Write(body)
	return hmac.Equal(given, mac.Sum(nil))
}

func (a *App) sign(query url.Values) []byte {
	keys := make([]string, 0, len(query))
	for key := range query {
		if key != "hmac" && key != "signature" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	pairs := make([]string, len(keys))
	for i, key := range keys {
		pairs[i] = key + "=" + strings.Join(query[key], ",")
	}
	mac := hmac.New(sha256.New, []byte(a.ClientSecret))
	mac.Write([]byte(strings.Join(pairs, "&")))
	return mac.Sum(nil)
}

// Token is a store's access: an access token (an hour) and a refresh token (90 days) to get the next.
type Token struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	Scopes           []string // what the store granted
}

// HasScopes reports whether the store granted everything Plant Stock asks for.
func (t Token) HasScopes() bool {
	granted := map[string]bool{}
	for _, scope := range t.Scopes {
		granted[scope] = true
		if read, ok := strings.CutPrefix(scope, "write_"); ok {
			granted["read_"+read] = true
		}
	}
	for _, scope := range Scopes {
		if !granted[scope] {
			return false
		}
	}
	return true
}

// ExchangeCode trades the one-time code from the callback for the store's tokens.
func (a *App) ExchangeCode(ctx context.Context, shop, code string) (Token, error) {
	return a.requestToken(ctx, shop, url.Values{
		"client_id": {a.ClientID}, "client_secret": {a.ClientSecret}, "code": {code}, "expiring": {"1"},
	})
}

// Refresh gets a new access token (and a new refresh token; the old one stops working).
// It fails with ErrReconnect if Shopify refuses, or ErrUnavailable.
func (a *App) Refresh(ctx context.Context, shop, refreshToken string) (Token, error) {
	return a.requestToken(ctx, shop, url.Values{
		"client_id": {a.ClientID}, "client_secret": {a.ClientSecret},
		"grant_type": {"refresh_token"}, "refresh_token": {refreshToken},
	})
}

func (a *App) requestToken(ctx context.Context, shop string, form url.Values) (Token, error) {
	if !ValidShop(shop) {
		return Token{}, fmt.Errorf("shopify: invalid shop %q", shop)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL(shop)+"/admin/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")

	var body struct {
		AccessToken           string `json:"access_token"`
		ExpiresIn             int    `json:"expires_in"` // seconds
		RefreshToken          string `json:"refresh_token"`
		RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
		Scope                 string `json:"scope"`
	}
	if err := a.do(request, &body); err != nil {
		return Token{}, fmt.Errorf("shopify token: %w", err)
	}
	if body.AccessToken == "" || body.RefreshToken == "" {
		return Token{}, errors.New("shopify token: response has no expiring tokens")
	}
	now := time.Now()
	return Token{
		AccessToken:      body.AccessToken,
		AccessExpiresAt:  now.Add(time.Duration(body.ExpiresIn) * time.Second),
		RefreshToken:     body.RefreshToken,
		RefreshExpiresAt: now.Add(time.Duration(body.RefreshTokenExpiresIn) * time.Second),
		Scopes:           strings.Split(body.Scope, ","),
	}, nil
}

// GraphQL runs an Admin API query for shop and decodes its data into out. It fails with
// ErrReconnect if Shopify refuses the token, or ErrUnavailable.
func (a *App) GraphQL(ctx context.Context, shop, accessToken, query string, variables map[string]any, out any) error {
	if !ValidShop(shop) {
		return fmt.Errorf("shopify: invalid shop %q", shop)
	}
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	endpoint := a.baseURL(shop) + "/admin/api/" + APIVersion + "/graphql.json"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Shopify-Access-Token", accessToken)

	var body struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := a.do(request, &body); err != nil {
		return fmt.Errorf("shopify graphql: %w", err)
	}
	if len(body.Errors) > 0 {
		if body.Errors[0].Extensions.Code == "THROTTLED" {
			return fmt.Errorf("shopify graphql: %w", ErrUnavailable)
		}
		return fmt.Errorf("shopify graphql: %s", body.Errors[0].Message)
	}
	return json.Unmarshal(body.Data, out)
}

// do sends request and decodes a successful JSON response into out.
func (a *App) do(request *http.Request, out any) error {
	response, err := a.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 10<<20))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	switch {
	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		return fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
		// A refused refresh token comes back as 400 invalid_grant (or invalid_request).
		response.StatusCode == http.StatusBadRequest && bytes.Contains(body, []byte("invalid_")):
		return fmt.Errorf("%w: status %d", ErrReconnect, response.StatusCode)
	case response.StatusCode != http.StatusOK:
		return fmt.Errorf("status %d: %.200s", response.StatusCode, body)
	}
	return json.Unmarshal(body, out)
}
