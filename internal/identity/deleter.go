package identity

import (
	"context"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

// Deleter is the account-deletion seam into the identity provider. nil in
// Deps means local-only deletion (no provider configured); DevDeleter means
// the dev bypass (nothing upstream exists to delete).
type Deleter interface {
	DeleteUser(ctx context.Context, clerkUserID string) error
}

// DevDeleter satisfies the seam for DEV_AUTH_BYPASS: the user row is a local
// mirror with no Clerk counterpart upstream.
type DevDeleter struct{}

func (DevDeleter) DeleteUser(context.Context, string) error { return nil }

// clerkDeleter deletes the user through Clerk's Backend API. This is the only
// place the user-delete SDK call is touched (fetcher.go owns reads).
type clerkDeleter struct {
	client *user.Client
}

// NewClerkDeleter builds the production Deleter from CLERK_SECRET_KEY.
func NewClerkDeleter(secretKey string) Deleter {
	return &clerkDeleter{client: user.NewClient(&clerk.ClientConfig{
		BackendConfig: clerk.BackendConfig{Key: &secretKey},
	})}
}

func (d *clerkDeleter) DeleteUser(ctx context.Context, clerkUserID string) error {
	_, err := d.client.Delete(ctx, clerkUserID)
	return err
}
