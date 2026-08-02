// Package identity owns the auth seam: the Verifier interface (Clerk is the
// ONLY file touching clerk-sdk-go), context keys, Require* middleware, and
// Clerk webhook mirror sync.
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

// Claims is the provider-agnostic session identity.
type Claims struct {
	UserID, OrgID, OrgRole, OrgSlug string
}

// Verifier validates a session token into Claims.
type Verifier interface {
	Verify(ctx context.Context, token string) (*Claims, error)
}

// ClerkVerifier verifies __session JWTs against Clerk's JWKS. This performs
// the same verification as clerkhttp.WithHeaderAuthorization (decode → JWK →
// verify), kept behind the Verifier seam so handlers never see the SDK.
type ClerkVerifier struct {
	jwksClient *jwks.Client
}

func NewClerkVerifier(secretKey string) *ClerkVerifier {
	return &ClerkVerifier{
		jwksClient: jwks.NewClient(&clerk.ClientConfig{
			BackendConfig: clerk.BackendConfig{Key: &secretKey},
		}),
	}
}

func (v *ClerkVerifier) Verify(ctx context.Context, token string) (*Claims, error) {
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token:      token,
		JWKSClient: v.jwksClient,
		Leeway:     10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	return &Claims{
		UserID:  claims.Subject,
		OrgID:   claims.ActiveOrganizationID,
		OrgRole: claims.ActiveOrganizationRole,
		OrgSlug: claims.ActiveOrganizationSlug,
	}, nil
}

// FakeVerifier accepts synthetic tokens of the exact shape
// "e2e:<userID>:<orgID>:<role>" (empty orgID = no active org). It is wired
// ONLY when DEV_AUTH_BYPASS=true (refused in production) and powers the e2e
// harness: every guard and middleware still executes against real claims.
type FakeVerifier struct{}

var ErrInvalidToken = errors.New("invalid session token")

func (FakeVerifier) Verify(_ context.Context, token string) (*Claims, error) {
	parts := strings.SplitN(token, ":", 4)
	if len(parts) != 4 || parts[0] != "e2e" || parts[1] == "" {
		return nil, fmt.Errorf("%w: want e2e:<userID>:<orgID>:<role>", ErrInvalidToken)
	}
	return &Claims{UserID: parts[1], OrgID: parts[2], OrgRole: parts[3]}, nil
}
