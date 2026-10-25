package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Store saves signed-in users. db is the connection pool, or a transaction in tests.
type Store struct {
	q *dbgen.Queries
}

func NewStore(db dbgen.DBTX) *Store {
	return &Store{q: dbgen.New(db)}
}

// SyncUser returns the local user for a signed-in identity, creating it on first sign-in and
// updating the email and name when they change in Clerk. Unchanged users cost one read, no write.
func (s *Store) SyncUser(ctx context.Context, identity Identity) (dbgen.User, error) {
	email := strings.ToLower(strings.TrimSpace(identity.Email))
	var name *string
	if trimmed := strings.TrimSpace(identity.Name); trimmed != "" {
		name = &trimmed
	}

	user, err := s.q.GetUserByExternalID(ctx, identity.Subject)
	if err == nil && user.Email == email && (name == nil || (user.DisplayName != nil && *user.DisplayName == *name)) {
		return user, nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.User{}, fmt.Errorf("get user: %w", err)
	}

	user, err = s.q.UpsertUser(ctx, dbgen.UpsertUserParams{ExternalID: identity.Subject, Email: email, DisplayName: name})
	if err != nil {
		return dbgen.User{}, fmt.Errorf("save user: %w", err)
	}
	return user, nil
}
