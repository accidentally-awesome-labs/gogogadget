package identity

import (
	"context"
	"fmt"
	"strings"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/user"
)

// UserProfile is the minimal mirror payload.
type UserProfile struct {
	Email, Name, AvatarURL string
}

// UserFetcher loads a user profile from the identity provider (lazy upsert).
type UserFetcher interface {
	Fetch(ctx context.Context, userID string) (UserProfile, error)
}

// ClerkUserFetcher reads users from the Clerk API.
type ClerkUserFetcher struct {
	client *user.Client
}

func NewClerkUserFetcher(secretKey string) *ClerkUserFetcher {
	return &ClerkUserFetcher{
		client: user.NewClient(&clerk.ClientConfig{
			BackendConfig: clerk.BackendConfig{Key: &secretKey},
		}),
	}
}

func (f *ClerkUserFetcher) Fetch(ctx context.Context, userID string) (UserProfile, error) {
	u, err := f.client.Get(ctx, userID)
	if err != nil {
		return UserProfile{}, err
	}
	email := ""
	if u.PrimaryEmailAddressID != nil {
		for _, e := range u.EmailAddresses {
			if e.ID == *u.PrimaryEmailAddressID {
				email = e.EmailAddress
				break
			}
		}
	}
	if email == "" && len(u.EmailAddresses) > 0 {
		email = u.EmailAddresses[0].EmailAddress
	}
	return UserProfile{
		Email:     email,
		Name:      DisplayName(u.FirstName, u.LastName, email),
		AvatarURL: deref(u.ImageURL),
	}, nil
}

// DevUserFetcher synthesizes profiles for DEV_AUTH_BYPASS tokens — no Clerk
// account involved. Only wired when the bypass is on.
type DevUserFetcher struct{}

func (DevUserFetcher) Fetch(_ context.Context, userID string) (UserProfile, error) {
	name := userID
	email := userID + "@gogogadget.dev"
	if !strings.HasPrefix(userID, "user_") {
		return UserProfile{}, fmt.Errorf("dev fetcher: unexpected user id %q", userID)
	}
	return UserProfile{Email: email, Name: name, AvatarURL: ""}, nil
}

// DisplayName prefers "First Last", falls back to the email prefix.
func DisplayName(first, last *string, email string) string {
	name := strings.TrimSpace(deref(first) + " " + deref(last))
	if name != "" {
		return name
	}
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i]
	}
	return email
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
