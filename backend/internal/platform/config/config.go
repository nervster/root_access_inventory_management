// Package config reads the API's settings from environment variables.
// Defaults are for local development (docker compose); production sets every variable.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/mail"
	"os"
	"strings"

	"github.com/joho/godotenv"

	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
)

type Config struct {
	Port        string
	DatabaseURL string

	// ClerkSecretKey (sk_…) lets the API fetch Clerk's token-signing keys. Required, never committed.
	ClerkSecretKey string

	// AppURL is the web app's public URL. Session tokens are only accepted when issued to it,
	// and emailed links point to it.
	AppURL string

	// Outgoing email. Locally: Mailpit (docker compose), no username or password.
	SMTPAddr     string // host:port
	SMTPUsername string
	SMTPPassword string
	EmailFrom    string

	// PlatformAdminEmails may create and suspend nurseries (comma-separated in PLATFORM_ADMIN_EMAILS).
	PlatformAdminEmails []string

	// Photo storage (S3-compatible): Google Cloud Storage when deployed (STORAGE_ENDPOINT
	// https://storage.googleapis.com, STORAGE_REGION auto, and its HMAC keys); locally the SeaweedFS
	// stand-in (STORAGE_ENDPOINT http://localhost:8333, no keys). See internal/platform/storage.
	StorageBucket          string
	StorageRegion          string
	StorageEndpoint        string
	StoragePublicEndpoint  string // what browsers use in signed links, if different from STORAGE_ENDPOINT
	StorageAccessKeyID     string
	StorageSecretAccessKey string

	// Shopify app (Dev Dashboard → the app → Settings). Without a client ID, Shopify is turned off.
	ShopifyClientID     string
	ShopifyClientSecret string
	ShopifyTokenKey     string // encrypts stores' tokens: openssl rand -base64 32
}

// Load reads settings from environment variables. For local development it first loads .env
// files if they exist (backend/.env, then the repo root's .env); real environment variables win.
func Load() (Config, error) {
	for _, file := range []string{".env", "../.env"} {
		if err := godotenv.Load(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("read %s: %w", file, err)
		}
	}

	cfg := Config{
		Port:           getEnv("PORT", "8080"),
		DatabaseURL:    getEnv("DATABASE_URL", "postgres://nms:nms_dev@localhost:5433/nms?sslmode=disable"),
		ClerkSecretKey: os.Getenv("CLERK_SECRET_KEY"),
		AppURL:         getEnv("APP_URL", "http://localhost:5173"),
		SMTPAddr:       getEnv("SMTP_ADDR", "localhost:1025"),
		SMTPUsername:   os.Getenv("SMTP_USERNAME"),
		SMTPPassword:   os.Getenv("SMTP_PASSWORD"),
		EmailFrom:      getEnv("EMAIL_FROM", "Plant Stock <no-reply@localhost>"),

		StorageBucket:          getEnv("STORAGE_BUCKET", "nms-photos"),
		StorageRegion:          getEnv("STORAGE_REGION", "us-east-1"),
		StorageEndpoint:        os.Getenv("STORAGE_ENDPOINT"),
		StoragePublicEndpoint:  os.Getenv("STORAGE_PUBLIC_ENDPOINT"),
		StorageAccessKeyID:     os.Getenv("STORAGE_ACCESS_KEY_ID"),
		StorageSecretAccessKey: os.Getenv("STORAGE_SECRET_ACCESS_KEY"),

		ShopifyClientID:     os.Getenv("SHOPIFY_CLIENT_ID"),
		ShopifyClientSecret: os.Getenv("SHOPIFY_CLIENT_SECRET"),
		ShopifyTokenKey:     os.Getenv("SHOPIFY_TOKEN_KEY"),
	}
	if cfg.ClerkSecretKey == "" {
		return Config{}, errors.New("CLERK_SECRET_KEY is required (locally: add it to the repo's .env)")
	}

	if cfg.ShopifyClientID != "" && (cfg.ShopifyClientSecret == "" || cfg.ShopifyTokenKey == "") {
		return Config{}, errors.New("SHOPIFY_CLIENT_ID is set, so SHOPIFY_CLIENT_SECRET and SHOPIFY_TOKEN_KEY are required too " +
			"(make the key with: openssl rand -base64 32)")
	}

	// A typo here would silently lock the admin out, so refuse to start instead.
	for _, email := range strings.Split(os.Getenv("PLATFORM_ADMIN_EMAILS"), ",") {
		if email = strings.TrimSpace(email); email == "" {
			continue
		}
		if address, err := mail.ParseAddress(email); err != nil || address.Address != email {
			return Config{}, fmt.Errorf("PLATFORM_ADMIN_EMAILS: %q is not an email address", email)
		}
		cfg.PlatformAdminEmails = append(cfg.PlatformAdminEmails, email)
	}
	return cfg, nil
}

// Storage is where photos are kept.
func (c Config) Storage() storage.Config {
	return storage.Config{
		Bucket: c.StorageBucket, Region: c.StorageRegion,
		Endpoint: c.StorageEndpoint, PublicEndpoint: c.StoragePublicEndpoint,
		AccessKeyID: c.StorageAccessKeyID, SecretAccessKey: c.StorageSecretAccessKey,
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
