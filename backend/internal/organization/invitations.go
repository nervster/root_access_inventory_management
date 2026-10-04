package organization

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/nervster/root_access_inventory_management/backend/internal/db"
	"github.com/nervster/root_access_inventory_management/backend/internal/db/dbgen"
	"github.com/nervster/root_access_inventory_management/backend/internal/platform/email"
)

var (
	ErrAlreadyMember  = errors.New("already a member of this organization")
	ErrAlreadyInvited = errors.New("already has a pending invitation")
	ErrInvitationGone = errors.New("invitation is no longer valid")
	ErrSuspended      = errors.New("organization is suspended")
)

const invitationLifetime = 7 * 24 * time.Hour

func normalizeEmail(address string) string {
	return strings.ToLower(strings.TrimSpace(address))
}

// --- Inside one nursery ---

// Invitations lists orgID's open invitations (expired ones included, so they can be resent), newest first.
func (s *Store) Invitations(ctx context.Context, orgID int64) (invitations []dbgen.Invitation, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		invitations, err = q.ListOpenInvitations(ctx, orgID)
		return err
	})
	return invitations, err
}

// CreateInvitation invites address to join orgID with role. invitedBy is nil when a platform admin
// invites. It fails with ErrAlreadyMember or ErrAlreadyInvited.
func (s *Store) CreateInvitation(ctx context.Context, orgID int64, address string, role dbgen.OrgRole, invitedBy *int64) (invitation dbgen.Invitation, err error) {
	address = normalizeEmail(address)
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		member, err := q.IsMemberByEmail(ctx, dbgen.IsMemberByEmailParams{OrganizationID: orgID, Email: address})
		if err != nil {
			return err
		}
		if member {
			return ErrAlreadyMember
		}

		open, err := q.GetOpenInvitationByEmail(ctx, dbgen.GetOpenInvitationByEmailParams{OrganizationID: orgID, Email: address})
		switch {
		case err == nil && open.ExpiresAt.After(time.Now()):
			return ErrAlreadyInvited
		case err == nil:
			// Expired, but it still holds the "one open invitation per email" slot; retire it.
			if err := q.RevokeInvitation(ctx, dbgen.RevokeInvitationParams{OrganizationID: orgID, ID: open.ID}); err != nil {
				return err
			}
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}

		invitation, err = q.CreateInvitation(ctx, dbgen.CreateInvitationParams{
			OrganizationID:  orgID,
			Email:           address,
			Role:            role,
			InvitedByUserID: invitedBy,
			ExpiresAt:       time.Now().Add(invitationLifetime),
		})
		if isUniqueViolation(err) { // a parallel request invited the same email first
			return ErrAlreadyInvited
		}
		return err
	})
	return invitation, err
}

// OpenInvitation returns an invitation that isn't accepted or revoked yet, or ErrNotFound.
func (s *Store) OpenInvitation(ctx context.Context, orgID, invitationID int64) (invitation dbgen.Invitation, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		invitation, err = q.GetOpenInvitation(ctx, dbgen.GetOpenInvitationParams{OrganizationID: orgID, ID: invitationID})
		return notFound(err)
	})
	return invitation, err
}

// ExtendInvitation gives an invitation a fresh expiry date (for resending).
func (s *Store) ExtendInvitation(ctx context.Context, orgID, invitationID int64) (invitation dbgen.Invitation, err error) {
	err = db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		invitation, err = q.ExtendInvitation(ctx, dbgen.ExtendInvitationParams{
			OrganizationID: orgID, ID: invitationID, ExpiresAt: time.Now().Add(invitationLifetime),
		})
		return notFound(err)
	})
	return invitation, err
}

func (s *Store) RevokeInvitation(ctx context.Context, orgID, invitationID int64) error {
	return db.InTenant(ctx, s.conn, orgID, func(q *dbgen.Queries) error {
		return q.RevokeInvitation(ctx, dbgen.RevokeInvitationParams{OrganizationID: orgID, ID: invitationID})
	})
}

// --- Account level: invitations addressed to one person ---

// PendingInvitationsFor lists invitations to address that can still be accepted.
func (s *Store) PendingInvitationsFor(ctx context.Context, address string) ([]dbgen.ListPendingInvitationsForEmailRow, error) {
	return dbgen.New(s.conn).ListPendingInvitationsForEmail(ctx, normalizeEmail(address))
}

// AcceptInvitation adds user to the invitation's organization and returns the organization's ID.
// Only invitations sent to the user's own email count. Fails with ErrNotFound, ErrSuspended,
// or ErrInvitationGone (already accepted, revoked, or expired).
func (s *Store) AcceptInvitation(ctx context.Context, invitationID int64, user dbgen.User) (int64, error) {
	invitation, err := s.invitationFor(ctx, invitationID, user.Email)
	if err != nil {
		return 0, err
	}
	if invitation.OrganizationStatus == dbgen.OrganizationStatusSuspended {
		return 0, ErrSuspended
	}

	err = db.InTenant(ctx, s.conn, invitation.OrganizationID, func(q *dbgen.Queries) error {
		accepted, err := q.AcceptInvitation(ctx, dbgen.AcceptInvitationParams{OrganizationID: invitation.OrganizationID, ID: invitation.ID})
		if err != nil {
			return err
		}
		if accepted == 0 {
			return ErrInvitationGone
		}
		return q.JoinOrganization(ctx, dbgen.JoinOrganizationParams{
			OrganizationID: invitation.OrganizationID, UserID: user.ID, Role: invitation.Role,
		})
	})
	return invitation.OrganizationID, err
}

// DeclineInvitation turns down an invitation sent to the user's own email.
func (s *Store) DeclineInvitation(ctx context.Context, invitationID int64, user dbgen.User) error {
	invitation, err := s.invitationFor(ctx, invitationID, user.Email)
	if err != nil {
		return err
	}
	return s.RevokeInvitation(ctx, invitation.OrganizationID, invitation.ID)
}

func (s *Store) invitationFor(ctx context.Context, invitationID int64, address string) (dbgen.GetInvitationForEmailRow, error) {
	invitation, err := dbgen.New(s.conn).GetInvitationForEmail(ctx, dbgen.GetInvitationForEmailParams{ID: invitationID, Email: normalizeEmail(address)})
	return invitation, notFound(err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// --- Delivery ---

// SignUpInviter lets someone new create an account. auth.ClerkUsers is the real one.
type SignUpInviter interface {
	InviteToSignUp(ctx context.Context, email, redirectURL string) (registered bool, err error)
}

// Deliverer sends invitations. People new to NMS get a sign-up invitation from Clerk (sign-up is
// invite-only); people who already have an account get an email from us.
type Deliverer struct {
	SignUp SignUpInviter
	Email  email.Sender
	AppURL string // the web app, for links
}

func (d Deliverer) Deliver(ctx context.Context, invitation dbgen.Invitation, orgName string) error {
	appURL := strings.TrimRight(d.AppURL, "/")
	registered, err := d.SignUp.InviteToSignUp(ctx, invitation.Email, appURL+"/sign-up")
	if err != nil || !registered {
		return err
	}

	return d.Email.Send(ctx, email.Message{
		To:      invitation.Email,
		Subject: fmt.Sprintf("You're invited to join %s", orgName),
		Text: fmt.Sprintf(`You've been invited to join %s on NMS (Nursery Management System) as %s.

Sign in to accept: %s/

This invitation expires %s.
`, orgName, invitation.Role, appURL, invitation.ExpiresAt.Format("January 2, 2006")),
	})
}
