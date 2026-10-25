package auth

import (
	"context"
	"fmt"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/invitation"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

// ClerkUsers talks to Clerk's Backend API about accounts (https://clerk.com/docs/reference/backend-api).
type ClerkUsers struct {
	users       *user.Client
	invitations *invitation.Client
}

func NewClerkUsers(secretKey string) *ClerkUsers {
	config := &clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}}
	return &ClerkUsers{users: user.NewClient(config), invitations: invitation.NewClient(config)}
}

// InviteToSignUp lets email create an account (sign-up is invite-only) and has Clerk email them a
// sign-up link that returns to redirectURL. If the email already has an account it sends nothing
// and returns registered = true, so the caller can tell them some other way.
func (c *ClerkUsers) InviteToSignUp(ctx context.Context, email, redirectURL string) (registered bool, err error) {
	existing, err := c.users.List(ctx, &user.ListParams{EmailAddresses: []string{email}})
	if err != nil {
		return false, fmt.Errorf("look up Clerk user: %w", err)
	}
	if len(existing.Users) > 0 {
		return true, nil
	}

	_, err = c.invitations.Create(ctx, &invitation.CreateParams{
		EmailAddress:   email,
		RedirectURL:    &redirectURL,
		Notify:         clerk.Bool(true),
		IgnoreExisting: clerk.Bool(true), // resending while Clerk's invite is still pending is fine
	})
	if err != nil {
		return false, fmt.Errorf("create Clerk invitation: %w", err)
	}
	return false, nil
}
