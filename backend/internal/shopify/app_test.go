package shopify

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"testing"
)

func TestNormalizeShop(t *testing.T) {
	for input, want := range map[string]string{
		"root-access-htx":                        "root-access-htx.myshopify.com",
		" Root-Access-HTX.myshopify.com ":        "root-access-htx.myshopify.com",
		"https://root-access-htx.myshopify.com/": "root-access-htx.myshopify.com",
		"root-access-htx.myshopify.com/admin":    "root-access-htx.myshopify.com",
		"evil.com":                               "",
		"root-access-htx.myshopify.com.evil.com": "",
		"-leading-dash":                          "",
		"two words":                              "",
		"":                                       "",
	} {
		if got := NormalizeShop(input); got != want {
			t.Errorf("NormalizeShop(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHasScopes(t *testing.T) {
	all := Token{Scopes: Scopes}
	if !all.HasScopes() {
		t.Error("every scope granted, want true")
	}
	// Shopify may list read scopes too; a write scope covers its read scope.
	withReads := Token{Scopes: append([]string{"read_products"}, Scopes...)}
	if !withReads.HasScopes() {
		t.Error("extra read scopes, want true")
	}
	if (Token{Scopes: Scopes[1:]}).HasScopes() {
		t.Error("a scope missing, want false")
	}
}

func TestSealer(t *testing.T) {
	sealer, err := NewSealer(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	sealed := sealer.Seal("a.myshopify.com", "shpat_secret")
	if bytes.Contains(sealed, []byte("shpat_secret")) {
		t.Error("sealed token contains the token")
	}
	if got, err := sealer.Open("a.myshopify.com", sealed); err != nil || got != "shpat_secret" {
		t.Errorf("Open = %q, %v; want the token", got, err)
	}
	if _, err := sealer.Open("b.myshopify.com", sealed); err == nil {
		t.Error("opened another store's token, want an error")
	}
	if bytes.Equal(sealed, sealer.Seal("a.myshopify.com", "shpat_secret")) {
		t.Error("sealing twice gave the same bytes, want a fresh nonce each time")
	}

	other, _ := NewSealer(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32)))
	if _, err := other.Open("a.myshopify.com", sealed); err == nil {
		t.Error("opened with another key, want an error")
	}
	for _, key := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("too short"))} {
		if _, err := NewSealer(key); err == nil {
			t.Errorf("NewSealer(%q) succeeded, want an error", key)
		}
	}
}

// Shopify's worked example (shopify.dev, Authenticate app proxies): the text it signs for these
// parameters. (Its example signature can't be checked: the docs replaced the store's name with {shop}.)
func TestProxyMessageMatchesShopifysExample(t *testing.T) {
	query, err := url.ParseQuery("extra=1&extra=2&shop={shop}.myshopify.com&logged_in_customer_id=&path_prefix=%2Fapps%2Fawesome_reviews&timestamp=1317327555&signature=e072b6d7e6622d85912a5214b860d3100dc1e73d9bc29f43796ac8c9ff8093cb")
	if err != nil {
		t.Fatal(err)
	}
	want := "extra=1,2logged_in_customer_id=path_prefix=/apps/awesome_reviewsshop={shop}.myshopify.comtimestamp=1317327555"
	if got := proxyMessage(query); got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
