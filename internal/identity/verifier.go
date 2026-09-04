// Package identity defines provider-neutral identity ports. Provider adapters
// (Clerk, the development adapter, and future implementations) are the only
// code that knows how a subject is encoded by an upstream identity service,
// how its webhook deliveries are signed, or how its payloads are shaped.
package identity

import (
	"context"
	"errors"
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

// ErrInvalidToken is the shared refusal every adapter returns for a session
// token or subject it cannot verify.
var ErrInvalidToken = errors.New("invalid session token")
