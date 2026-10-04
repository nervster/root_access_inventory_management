// Package config reads the API's settings from environment variables.
// Defaults are for local development (docker compose); production sets every variable.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/joho/godotenv"
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
		EmailFrom:      getEnv("EMAIL_FROM", "NMS <no-reply@localhost>"),

		PlatformAdminEmails: strings.Split(os.Getenv("PLATFORM_ADMIN_EMAILS"), ","),
	}
	if cfg.ClerkSecretKey == "" {
		return Config{}, errors.New("CLERK_SECRET_KEY is required (locally: add it to the repo's .env)")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
