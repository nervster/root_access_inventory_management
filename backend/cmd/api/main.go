// Command api runs the Plant Stock HTTP API.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // time zone database built into the binary; the production image has none

	"github.com/gin-gonic/gin"

	"github.com/nervster/root_access_inventory_management/backend/internal/auth"
	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/inventory"
	"github.com/nervster/root_access_inventory_management/backend/internal/orders"
	"github.com/nervster/root_access_inventory_management/backend/internal/organization"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/config"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/email"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/storage"
	"github.com/nervster/root_access_inventory_management/backend/internal/shopify"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api failed", "error", err)
		os.Exit(1)
	}
}

// run starts the API and blocks until it stops. Returning (instead of exiting) lets every defer run.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Stop on Ctrl+C locally, or SIGTERM when Kubernetes stops the container.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	storageConfig := cfg.Storage()
	photos, err := storage.New(ctx, storageConfig)
	if err != nil {
		return err
	}
	if storageConfig.Local() {
		// Locally, make the stand-in's bucket. If the stand-in isn't running, the API still starts;
		// only photos won't work.
		if err := photos.PrepareLocalBucket(ctx, []string{cfg.AppURL}); err != nil {
			slog.Warn("photo storage unavailable (is the storage stand-in running? docker compose up -d)", "error", err)
		}
	}

	shopifyStore, err := shopify.FromConfig(cfg, pool, photos)
	if err != nil {
		return err
	}

	deliverer := organization.Deliverer{
		SignUp: auth.NewClerkUsers(cfg.ClerkSecretKey),
		Email:  email.SMTP{Addr: cfg.SMTPAddr, From: cfg.EmailFrom, Username: cfg.SMTPUsername, Password: cfg.SMTPPassword},
		AppURL: cfg.AppURL,
	}
	router := newRouter(dependencies{
		authenticate:   auth.Clerk(cfg.ClerkSecretKey, []string{cfg.AppURL}),
		users:          auth.NewStore(pool),
		platformAdmins: auth.NewPlatformAdmins(cfg.PlatformAdminEmails),
		orgs:           organization.NewStore(pool),
		deliverer:      deliverer,
		inventory:      inventory.NewStore(pool),
		photos:         photos,
		shopify:        shopifyStore,
		conn:           pool,
		appURL:         cfg.AppURL,
	})

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("api listening", "port", cfg.Port)
		serverErr <- server.ListenAndServe()
	}()

	// Wait for the server to fail (e.g. the port is taken) or for a stop signal.
	select {
	case err := <-serverErr:
		return fmt.Errorf("server: %w", err)
	case <-ctx.Done():
	}

	// Give in-flight requests up to 10 seconds to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	slog.Info("api stopped")
	return nil
}

// dependencies are what the routes need, built in run (or by tests).
type dependencies struct {
	authenticate   gin.HandlerFunc // verifies session tokens: Clerk when running, a stand-in in tests
	users          *auth.Store
	platformAdmins auth.PlatformAdmins
	orgs           *organization.Store
	deliverer      organization.Deliverer
	inventory      *inventory.Store
	photos         inventory.PhotoStorage
	shopify        *shopify.Store
	conn           db.Conn
	appURL         string
}

// newRouter builds the HTTP routes. It's separate from run so tests can use it.
func newRouter(d dependencies) *gin.Engine {
	router := gin.Default()

	api := router.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Everything in this group needs a signed-in user.
	signedIn := api.Group("", d.authenticate, auth.RequireUser(d.users, d.platformAdmins))
	organization.Routes(signedIn, d.orgs, d.deliverer)
	organization.PlatformRoutes(signedIn, d.orgs, d.deliverer, d.platformAdmins)
	inventory.Routes(signedIn, d.orgs, d.inventory, d.photos)
	shopify.Routes(api, signedIn, d.orgs, d.shopify, d.appURL)
	orders.Routes(signedIn, d.orgs, orders.NewStore(d.conn))

	return router
}
