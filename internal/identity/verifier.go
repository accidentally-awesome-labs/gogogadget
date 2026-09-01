// Package identity defines provider-neutral identity ports. Provider adapters
// (Clerk, the development verifier, and future implementations) are the only
// code that knows how a subject is encoded by an upstream identity service.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/jwt"
)

// Claims is the internal, provider-neutral session identity. IDs are opaque
// domain identifiers and must not be interpreted as provider subjects.
type Claims struct{ UserID, OrgID, OrgRole, OrgSlug string }

// ProviderClaims is the verified provider-facing identity returned by adapters.
type ProviderClaims struct {
	Provider, UserSubject, OrgSubject, OrgRole, OrgSlug string
}

type Verifier interface {
	Verify(context.Context, string) (*ProviderClaims, error)
}

type ClerkVerifier struct{ jwksClient *jwks.Client }

func NewClerkVerifier(secretKey string) *ClerkVerifier {
	return &ClerkVerifier{jwksClient: jwks.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{Key: &secretKey}})}
}
func (v *ClerkVerifier) Verify(ctx context.Context, token string) (*ProviderClaims, error) {
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{Token: token, JWKSClient: v.jwksClient, Leeway: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return &ProviderClaims{Provider: "clerk", UserSubject: claims.Subject, OrgSubject: claims.ActiveOrganizationID, OrgRole: claims.ActiveOrganizationRole, OrgSlug: claims.ActiveOrganizationSlug}, nil
}

type FakeVerifier struct{}

var ErrInvalidToken = errors.New("invalid session token")

func (FakeVerifier) Verify(_ context.Context, token string) (*ProviderClaims, error) {
	parts := strings.SplitN(token, ":", 4)
	if len(parts) != 4 || parts[0] != "e2e" || parts[1] == "" {
		return nil, fmt.Errorf("%w: want e2e:<userID>:<orgID>:<role>", ErrInvalidToken)
	}
	return &ProviderClaims{Provider: "dev", UserSubject: parts[1], OrgSubject: parts[2], OrgRole: parts[3], OrgSlug: parts[2]}, nil
}
