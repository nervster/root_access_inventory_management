// Package organization covers nurseries (organizations) and their teams: who belongs to which
// nursery, with what role, and what each role may do.
package organization

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrLastOwner = errors.New("an organization must keep at least one Owner")
)

// Store reads and changes organizations and their teams. Work inside one nursery goes through
// db.InTenant, so Postgres keeps it from reaching any other nursery.
type Store struct {
	conn db.Conn
}

func NewStore(conn db.Conn) *Store {
	return &Store{conn: conn}
}

// Access is a signed-in user's place in one organization.
type Access struct {
	OrganizationID int64
	MembershipID   int64
	Role           dbgen.OrgRole
	Suspended      bool // the organization is suspended
}

// Member is a person on an organization's team. Its fields match the generated
// dbgen.GetMemberRow and dbgen.ListMembersRow, so either converts with Member(row).
type Member struct {
	ID          int64
	UserID      int64
	Email       string
	DisplayName *string
	Role        dbgen.OrgRole
	CreatedAt   time.Time
}

// --- Account-level: about one user across nurseries, so not inside a tenant ---

// Access returns userID's access to orgID, or ErrNotFound if they aren't a member.
// It decides whether the user may enter the nursery at all, so it runs before any tenant is set.
func (s *Store) Access(ctx context.Context, orgID, userID int64) (Access, error) {
	row, err := dbgen.New(s.conn).GetAccess(ctx, dbgen.GetAccessParams{OrganizationID: orgID, UserID: userID})
	if err != nil {
		return Access{}, notFound(err)
	}
	return Access{
		OrganizationID: orgID,
		MembershipID:   row.MembershipID,
		Role:           row.Role,
		Suspended:      row.OrganizationStatus == dbgen.OrganizationStatusSuspended,
	}, nil
}

// MembershipsForUser lists every organization userID belongs to, by name.
func (s *Store) MembershipsForUser(ctx context.Context, userID int64) ([]dbgen.ListMembershipsForUserRow, error) {
	return dbgen.New(s.conn).ListMembershipsForUser(ctx, userID)
}

// --- Inside one nursery ---

func (s *Store) Organization(ctx context.Context, orgID int64) (org dbgen.Organization, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		org, err = q.GetOrganization(ctx, orgID)
		return notFound(err)
	})
	return org, err
}

// UpdateOrganization changes the fields that aren't nil.
func (s *Store) UpdateOrganization(ctx context.Context, orgID int64, name, timeZone *string) (org dbgen.Organization, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		org, err = q.UpdateOrganization(ctx, dbgen.UpdateOrganizationParams{ID: orgID, Name: name, TimeZone: timeZone})
		return notFound(err)
	})
	return org, err
}

// Members lists orgID's team, Owners first.
func (s *Store) Members(ctx context.Context, orgID int64) ([]Member, error) {
	var members []Member
	err := db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		rows, err := q.ListMembers(ctx, orgID)
		if err != nil {
			return err
		}
		members = make([]Member, len(rows))
		for i, row := range rows {
			members[i] = Member(row)
		}
		return nil
	})
	return members, err
}

func (s *Store) Member(ctx context.Context, orgID, memberID int64) (member Member, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		row, err := q.GetMember(ctx, dbgen.GetMemberParams{OrganizationID: orgID, ID: memberID})
		member = Member(row)
		return notFound(err)
	})
	return member, err
}

// ChangeRole gives a member a new role. It refuses (ErrLastOwner) to demote the only Owner.
func (s *Store) ChangeRole(ctx context.Context, orgID, memberID int64, role dbgen.OrgRole) (Member, error) {
	var member Member
	err := s.changeTeam(ctx, orgID, memberID, func(q *dbgen.Queries, current Member) error {
		if current.Role == dbgen.OrgRoleOwner && role != dbgen.OrgRoleOwner {
			if err := ensureAnotherOwner(ctx, q, orgID); err != nil {
				return err
			}
		}
		member = current
		member.Role = role
		return q.UpdateMemberRole(ctx, dbgen.UpdateMemberRoleParams{OrganizationID: orgID, ID: memberID, Role: role})
	})
	return member, err
}

// RemoveMember takes a member off the team. It refuses (ErrLastOwner) to remove the only Owner.
func (s *Store) RemoveMember(ctx context.Context, orgID, memberID int64) error {
	return s.changeTeam(ctx, orgID, memberID, func(q *dbgen.Queries, current Member) error {
		if current.Role == dbgen.OrgRoleOwner {
			if err := ensureAnotherOwner(ctx, q, orgID); err != nil {
				return err
			}
		}
		return q.DeleteMember(ctx, dbgen.DeleteMemberParams{OrganizationID: orgID, ID: memberID})
	})
}

// changeTeam runs change while holding a lock on the organization, so two changes can't both
// pass the "another Owner remains" check and leave the organization with none.
func (s *Store) changeTeam(ctx context.Context, orgID, memberID int64, change func(*dbgen.Queries, Member) error) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		if err := q.LockOrganization(ctx, orgID); err != nil {
			return fmt.Errorf("lock organization: %w", err)
		}
		row, err := q.GetMember(ctx, dbgen.GetMemberParams{OrganizationID: orgID, ID: memberID})
		if err != nil {
			return notFound(err)
		}
		return change(q, Member(row))
	})
}

func ensureAnotherOwner(ctx context.Context, q *dbgen.Queries, orgID int64) error {
	owners, err := q.CountOwners(ctx, orgID)
	if err != nil {
		return fmt.Errorf("count owners: %w", err)
	}
	if owners <= 1 {
		return ErrLastOwner
	}
	return nil
}

// notFound turns "no rows" into ErrNotFound and leaves other errors (and nil) as they are.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
