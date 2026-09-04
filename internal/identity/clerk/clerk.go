package clerk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
	"github.com/clerk/clerk-sdk-go/v2/organization"
	"github.com/clerk/clerk-sdk-go/v2/user"

	"github.com/gogogadget/gogogadget/internal/identity"
)

// Verifier verifies Clerk session JWTs against Clerk's JWKS and resolves
// Clerk subjects through the Clerk backend API. It is the only identity code
// that knows Clerk exists.
type Verifier struct {
	jwksClient *jwks.Client
	userClient *user.Client
	orgClient  *organization.Client
}

func NewVerifier(secretKey string) *Verifier {
	cfg := &clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}}
	return &Verifier{jwksClient: jwks.NewClient(cfg), userClient: user.NewClient(cfg), orgClient: organization.NewClient(cfg)}
}

func (v *Verifier) Verify(ctx context.Context, token string) (*identity.ProviderClaims, error) {
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{Token: token, JWKSClient: v.jwksClient, Leeway: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: claims.Subject, OrgSubject: claims.ActiveOrganizationID, OrgRole: claims.ActiveOrganizationRole, OrgSlug: claims.ActiveOrganizationSlug}, nil
}

func (v *Verifier) VerifySubject(ctx context.Context, subject string) (*identity.ProviderClaims, error) {
	if subject == "" || v == nil || v.userClient == nil {
		return nil, identity.ErrInvalidToken
	}
	u, err := v.userClient.Get(ctx, subject)
	if err != nil {
		return nil, err
	}
	if u.ID != subject {
		return nil, fmt.Errorf("identity clerk: verified subject mismatch")
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: subject}, nil
}

func (v *Verifier) VerifyOrganizationSubject(ctx context.Context, subject string) (*identity.ProviderClaims, error) {
	if subject == "" || v == nil || v.orgClient == nil {
		return nil, identity.ErrInvalidToken
	}
	org, err := v.orgClient.Get(ctx, subject)
	if err != nil {
		return nil, err
	}
	if org.ID != subject {
		return nil, fmt.Errorf("identity clerk: verified organization subject mismatch")
	}
	return &identity.ProviderClaims{Provider: Provider, OrgSubject: subject, OrgSlug: org.Slug}, nil
}

// UserFetcher reads one Clerk user record and flattens it into the neutral
// profile shape.
type UserFetcher struct{ client *user.Client }

func NewUserFetcher(secretKey string) *UserFetcher {
	return &UserFetcher{client: user.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})}
}

func (f *UserFetcher) Fetch(ctx context.Context, userSubject string) (identity.UserProfile, error) {
	u, err := f.client.Get(ctx, userSubject)
	if err != nil {
		return identity.UserProfile{}, err
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
	return identity.UserProfile{Email: email, Name: DisplayName(u.FirstName, u.LastName, email), AvatarURL: deref(u.ImageURL)}, nil
}

type deleter struct{ client *user.Client }

func NewDeleter(secretKey string) identity.Deleter {
	return &deleter{client: user.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})}
}

func (d *deleter) DeleteUser(ctx context.Context, userSubject string) error {
	_, err := d.client.Delete(ctx, userSubject)
	return err
}

// DisplayName collapses Clerk's optional first/last name pair into one label,
// falling back to the email local part.
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
