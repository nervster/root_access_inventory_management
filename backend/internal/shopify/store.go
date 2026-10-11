package shopify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/config"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/jobs"
)

var (
	ErrNotConfigured    = errors.New("shopify: not set up on this server")
	ErrNotConnected     = errors.New("shopify: no store connected")
	ErrConnectedToOther = errors.New("shopify: this nursery is connected to a different store")
	ErrOtherNursery     = errors.New("shopify: that store is connected to another nursery")
	ErrMissingScopes    = errors.New("shopify: the store didn't grant every permission")
	ErrBadState         = errors.New("shopify: unknown or used connection attempt")
	ErrExpired          = errors.New("shopify: the connection attempt expired")
	ErrLocationNotFound = errors.New("shopify: location not found")
)

// stateLifetime is how long an Owner has to approve the app on Shopify after clicking Connect.
const stateLifetime = 10 * time.Minute

// Store keeps nurseries' Shopify connections and makes Admin API calls for them.
type Store struct {
	conn   db.Conn
	app    *App // nil when Shopify isn't set up (no SHOPIFY_CLIENT_ID)
	sealer *Sealer
}

func NewStore(conn db.Conn, app *App, sealer *Sealer) *Store {
	return &Store{conn: conn, app: app, sealer: sealer}
}

// FromConfig sets up the Shopify app from the server's settings. Without SHOPIFY_CLIENT_ID,
// Shopify is off and Settings says so.
func FromConfig(cfg config.Config, conn db.Conn) (*Store, error) {
	if cfg.ShopifyClientID == "" {
		slog.Warn("Shopify is off (no SHOPIFY_CLIENT_ID)")
		return NewStore(conn, nil, nil), nil
	}
	sealer, err := NewSealer(cfg.ShopifyTokenKey)
	if err != nil {
		return nil, err
	}
	app := NewApp(cfg.ShopifyClientID, cfg.ShopifyClientSecret, strings.TrimSuffix(cfg.AppURL, "/")+callbackPath)
	return NewStore(conn, app, sealer), nil
}

// Configured reports whether the server has the Shopify app's credentials.
func (s *Store) Configured() bool { return s.app != nil }

// Connection returns orgID's connection. It fails with ErrNotConnected.
func (s *Store) Connection(ctx context.Context, orgID int64) (connection dbgen.ShopifyConnection, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		connection, err = q.GetShopifyConnection(ctx, orgID)
		return notConnected(err)
	})
	return connection, err
}

// StartConnect begins connecting shop to orgID (for the signed-in user in ctx): it returns
// Shopify's approval page and the state that must come back with the Owner. A nursery connected to
// one store must disconnect before connecting another (ErrConnectedToOther).
func (s *Store) StartConnect(ctx context.Context, orgID int64, shop string) (authorizeURL, state string, err error) {
	if s.app == nil {
		return "", "", ErrNotConfigured
	}
	random := make([]byte, 32)
	_, _ = rand.Read(random)
	state = base64.RawURLEncoding.EncodeToString(random)

	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		existing, err := q.GetShopifyConnection(ctx, orgID)
		if err == nil && existing.Shop != shop {
			return ErrConnectedToOther
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return q.CreateShopifyOAuthState(ctx, dbgen.CreateShopifyOAuthStateParams{State: state, OrganizationID: orgID, Shop: shop})
	})
	if err != nil {
		return "", "", err
	}
	return s.app.AuthorizeURL(shop, state), state, nil
}

// TakeState uses up a connection attempt, returning who started it for which nursery and store.
// It fails with ErrBadState (unknown or already used) or ErrExpired.
func (s *Store) TakeState(ctx context.Context, state string) (dbgen.ShopifyOauthState, error) {
	q := dbgen.New(s.conn) // account-level: the nursery isn't known until the state is found
	if err := q.DeleteOldShopifyOAuthStates(ctx); err != nil {
		return dbgen.ShopifyOauthState{}, err
	}
	attempt, err := q.TakeShopifyOAuthState(ctx, state)
	if errors.Is(err, pgx.ErrNoRows) {
		return attempt, ErrBadState
	} else if err != nil {
		return attempt, err
	}
	if time.Since(attempt.CreatedAt) > stateLifetime {
		return attempt, ErrExpired
	}
	return attempt, nil
}

// FinishConnect trades the callback's code for the store's tokens and saves them for the attempt's
// nursery. It fails with ErrMissingScopes, ErrOtherNursery, or a Shopify error.
func (s *Store) FinishConnect(ctx context.Context, attempt dbgen.ShopifyOauthState, code string) error {
	if s.app == nil {
		return ErrNotConfigured
	}
	token, err := s.app.ExchangeCode(ctx, attempt.Shop, code)
	if err != nil {
		return err
	}
	if !token.HasScopes() {
		return ErrMissingScopes
	}

	ctx = db.WithUser(ctx, attempt.UserID) // recorded as who connected it
	return db.InTenant(ctx, s.conn, attempt.OrganizationID, func(q *dbgen.Queries) error {
		_, err := q.SaveShopifyConnection(ctx, dbgen.SaveShopifyConnectionParams{
			OrganizationID:        attempt.OrganizationID,
			Shop:                  attempt.Shop,
			Scopes:                strings.Join(token.Scopes, ","),
			AccessToken:           s.sealer.Seal(attempt.Shop, token.AccessToken),
			AccessTokenExpiresAt:  token.AccessExpiresAt,
			RefreshToken:          s.sealer.Seal(attempt.Shop, token.RefreshToken),
			RefreshTokenExpiresAt: token.RefreshExpiresAt,
		})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation on shop
			return ErrOtherNursery
		} else if err != nil {
			return err
		}
		return jobs.Enqueue(ctx, q, attempt.OrganizationID, JobSyncAll, struct{}{}) // push the products
	})
}

// Disconnect forgets orgID's store (and its tokens). It fails with ErrNotConnected.
func (s *Store) Disconnect(ctx context.Context, orgID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		deleted, err := q.DeleteShopifyConnection(ctx, orgID)
		if err == nil && deleted == 0 {
			return ErrNotConnected
		}
		return err
	})
}

// Location is a place in the store that holds stock, such as the nursery's pickup spot.
type Location struct {
	ID   string `json:"id"` // gid://shopify/Location/123
	Name string `json:"name"`
}

// Locations lists the store's active locations.
func (s *Store) Locations(ctx context.Context, orgID int64) ([]Location, error) {
	var data struct {
		Locations struct {
			Nodes []Location `json:"nodes"`
		} `json:"locations"`
	}
	err := s.graphQL(ctx, orgID, `query Locations { locations(first: 50) { nodes { id name } } }`, nil, &data)
	return data.Locations.Nodes, err
}

// SetLocation chooses where customers pick up, and moves the stock on Shopify there. It fails
// with ErrLocationNotFound.
func (s *Store) SetLocation(ctx context.Context, orgID int64, locationID string) (Location, error) {
	locations, err := s.Locations(ctx, orgID)
	if err != nil {
		return Location{}, err
	}
	for _, location := range locations {
		if location.ID == locationID {
			return location, db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
				err := q.SetShopifyLocation(ctx, dbgen.SetShopifyLocationParams{OrganizationID: orgID, LocationID: &location.ID, LocationName: &location.Name})
				if err != nil {
					return err
				}
				return jobs.Enqueue(ctx, q, orgID, JobSyncAll, struct{}{}) // stock goes to the new location
			})
		}
	}
	return Location{}, ErrLocationNotFound
}

// graphQL runs an Admin API query for orgID's store. When Shopify refuses the token, the
// connection is marked as needing to be connected again.
func (s *Store) graphQL(ctx context.Context, orgID int64, query string, variables map[string]any, out any) error {
	shop, token, err := s.accessToken(ctx, orgID)
	if err != nil {
		return err
	}
	err = s.app.GraphQL(ctx, shop, token, query, variables, out)
	if errors.Is(err, ErrReconnect) {
		s.markNeedsReconnect(ctx, orgID)
	}
	return err
}

// accessToken returns a working access token for orgID's store, refreshing it when it has less
// than a minute left. The row stays locked during a refresh, so two requests never both refresh:
// the second waits, then uses the new token (the old refresh token stops working once used).
func (s *Store) accessToken(ctx context.Context, orgID int64) (shop, token string, err error) {
	if s.app == nil {
		return "", "", ErrNotConfigured
	}
	reconnect := false
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		connection, err := q.LockShopifyConnection(ctx, orgID)
		if err != nil {
			return notConnected(err)
		}
		if connection.NeedsReconnect {
			reconnect = true
			return nil
		}
		shop = connection.Shop
		if time.Until(connection.AccessTokenExpiresAt) > time.Minute {
			token, err = s.sealer.Open(shop, connection.AccessToken)
			return err
		}

		refreshToken, err := s.sealer.Open(shop, connection.RefreshToken)
		if err != nil {
			return err
		}
		fresh, err := s.app.Refresh(ctx, shop, refreshToken)
		if errors.Is(err, ErrReconnect) {
			reconnect = true
			return q.MarkShopifyNeedsReconnect(ctx, orgID)
		} else if err != nil {
			return err
		}
		token = fresh.AccessToken
		return q.UpdateShopifyTokens(ctx, dbgen.UpdateShopifyTokensParams{
			OrganizationID:        orgID,
			AccessToken:           s.sealer.Seal(shop, fresh.AccessToken),
			AccessTokenExpiresAt:  fresh.AccessExpiresAt,
			RefreshToken:          s.sealer.Seal(shop, fresh.RefreshToken),
			RefreshTokenExpiresAt: fresh.RefreshExpiresAt,
		})
	})
	if err == nil && reconnect {
		err = ErrReconnect
	}
	return shop, token, err
}

func (s *Store) markNeedsReconnect(ctx context.Context, orgID int64) {
	_ = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		return q.MarkShopifyNeedsReconnect(ctx, orgID)
	})
}

func notConnected(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotConnected
	}
	if err != nil {
		return fmt.Errorf("shopify connection: %w", err)
	}
	return nil
}
