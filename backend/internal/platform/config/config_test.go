package config

import "testing"

func TestLoadRequiresClerkSecretKey(t *testing.T) {
	t.Setenv("CLERK_SECRET_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() succeeded without CLERK_SECRET_KEY, want an error")
	}
}

func TestLoadRejectsInvalidPlatformAdminEmail(t *testing.T) {
	t.Setenv("CLERK_SECRET_KEY", "sk_test_example")
	t.Setenv("PLATFORM_ADMIN_EMAILS", "admin@example.com, admin2example.com")

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted admin2example.com, want an error")
	}
}

func TestLoadPlatformAdminEmails(t *testing.T) {
	t.Setenv("CLERK_SECRET_KEY", "sk_test_example")
	t.Setenv("PLATFORM_ADMIN_EMAILS", " a@example.com, ,b@example.com ")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.PlatformAdminEmails) != 2 || cfg.PlatformAdminEmails[0] != "a@example.com" || cfg.PlatformAdminEmails[1] != "b@example.com" {
		t.Errorf("PlatformAdminEmails = %q, want [a@example.com b@example.com]", cfg.PlatformAdminEmails)
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("CLERK_SECRET_KEY", "sk_test_example")
	t.Setenv("PORT", "")
	t.Setenv("APP_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.AppURL != "http://localhost:5173" {
		t.Errorf("AppURL = %q, want http://localhost:5173", cfg.AppURL)
	}
}
