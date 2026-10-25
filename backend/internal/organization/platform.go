package organization

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

// Platform admin: account data across nurseries (organizations, users, memberships). Nothing here
// reads a nursery's business data, and changes inside a nursery still go through db.InTenant.

var ErrSlugTaken = errors.New("slug is taken")

// OrganizationSummary is an organization with its member count. Its fields match the generated
// rows of ListOrganizationsWithMemberCounts and GetOrganizationWithMemberCount.
type OrganizationSummary struct {
	ID          int64
	Name        string
	Slug        string
	TimeZone    string
	Status      dbgen.OrganizationStatus
	CreatedAt   time.Time
	MemberCount int64
}

// AllOrganizations lists every nursery, by name.
func (s *Store) AllOrganizations(ctx context.Context) ([]OrganizationSummary, error) {
	rows, err := dbgen.New(s.conn).ListOrganizationsWithMemberCounts(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]OrganizationSummary, len(rows))
	for i, row := range rows {
		summaries[i] = OrganizationSummary(row)
	}
	return summaries, nil
}

func (s *Store) OrganizationSummary(ctx context.Context, orgID int64) (OrganizationSummary, error) {
	row, err := dbgen.New(s.conn).GetOrganizationWithMemberCount(ctx, orgID)
	return OrganizationSummary(row), notFound(err)
}

// CreateOrganization creates a nursery and invites its first Owner, together or not at all.
// It fails with ErrSlugTaken.
func (s *Store) CreateOrganization(ctx context.Context, name, slug, timeZone, ownerEmail string, invitedBy int64) (org dbgen.Organization, invitation dbgen.Invitation, err error) {
	err = pgx.BeginFunc(ctx, s.conn, func(tx pgx.Tx) error {
		org, err = dbgen.New(tx).CreateOrganization(ctx, dbgen.CreateOrganizationParams{Name: name, Slug: slug, TimeZone: timeZone})
		if isUniqueViolation(err) {
			return ErrSlugTaken
		}
		if err != nil {
			return err
		}
		invitation, err = NewStore(tx).CreateInvitation(ctx, org.ID, ownerEmail, dbgen.OrgRoleOwner, &invitedBy)
		return err
	})
	return org, invitation, err
}

// UpdateOrganizationAccount renames, suspends, or reactivates a nursery (nil keeps a value).
func (s *Store) UpdateOrganizationAccount(ctx context.Context, orgID int64, name *string, status *dbgen.OrganizationStatus) error {
	return dbgen.New(s.conn).UpdateOrganizationAccount(ctx, dbgen.UpdateOrganizationAccountParams{ID: orgID, Name: name, Status: status})
}

// UserWithMemberships is a user and every nursery they belong to.
type UserWithMemberships struct {
	dbgen.User
	Memberships []dbgen.ListMembershipsForUsersRow
}

// SearchUsers finds up to 50 users whose email contains term (all users when it's empty), newest first.
func (s *Store) SearchUsers(ctx context.Context, term string) ([]UserWithMemberships, error) {
	q := dbgen.New(s.conn)
	users, err := q.SearchUsers(ctx, normalizeEmail(term))
	if err != nil {
		return nil, err
	}

	ids := make([]int64, len(users))
	for i, user := range users {
		ids[i] = user.ID
	}
	memberships, err := q.ListMembershipsForUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	byUser := map[int64][]dbgen.ListMembershipsForUsersRow{}
	for _, m := range memberships {
		byUser[m.UserID] = append(byUser[m.UserID], m)
	}

	results := make([]UserWithMemberships, len(users))
	for i, user := range users {
		results[i] = UserWithMemberships{User: user, Memberships: byUser[user.ID]}
	}
	return results, nil
}
