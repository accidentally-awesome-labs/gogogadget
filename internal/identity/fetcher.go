package identity

import (
	"context"
	"fmt"
	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/user"
	"strings"
)

type UserProfile struct{ Email, Name, AvatarURL string }
type UserFetcher interface {
	Fetch(context.Context, string) (UserProfile, error)
}
type ClerkUserFetcher struct{ client *user.Client }

func NewClerkUserFetcher(secretKey string) *ClerkUserFetcher {
	return &ClerkUserFetcher{client: user.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})}
}
func (f *ClerkUserFetcher) Fetch(ctx context.Context, userSubject string) (UserProfile, error) {
	u, err := f.client.Get(ctx, userSubject)
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
	return UserProfile{Email: email, Name: DisplayName(u.FirstName, u.LastName, email), AvatarURL: deref(u.ImageURL)}, nil
}

type DevUserFetcher struct{}

func (DevUserFetcher) Fetch(_ context.Context, userSubject string) (UserProfile, error) {
	if !strings.HasPrefix(userSubject, "user_") {
		return UserProfile{}, fmt.Errorf("dev fetcher: unexpected user subject %q", userSubject)
	}
	return UserProfile{Email: userSubject + "@gogogadget.dev", Name: userSubject}, nil
}
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
