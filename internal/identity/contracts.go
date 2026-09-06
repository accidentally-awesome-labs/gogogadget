package identity

import (
	"context"
	"errors"
	"net/http"
)

var ErrLinkRequired = errors.New("identity: explicit account link required")

// ErrNoDestination reports that the identity adapter selected for this
// environment publishes no page for the destination a caller asked for.
//
// It exists so that "cannot serve this" is a value a caller must handle
// rather than an empty string it can ignore. An empty URL is
// indistinguishable from a working one until a visitor clicks it, and a
// provider path concatenated onto an empty base is worse still: it becomes a
// same-origin path this application does not serve.
var ErrNoDestination = errors.New("identity: adapter publishes no page for this destination")

// SubjectVerifier proves that an upstream user subject belongs to the
// selected provider adapter.
type SubjectVerifier interface {
	VerifySubject(context.Context, string) (*ProviderClaims, error)
}

// OrganizationSubjectVerifier proves an organization subject belongs to the
// hosted adapter. It is distinct from user subject verification.
type OrganizationSubjectVerifier interface {
	VerifyOrganizationSubject(context.Context, string) (*ProviderClaims, error)
}

// SyntheticSessionMinter is implemented by an identity adapter that can mint
// a session token for a subject with no upstream account. It is deliberately
// optional and deliberately not a slot capability: a slot's adapters must
// provide exactly the slot's capability set, and whether synthetic sessions
// exist is a property of the adapter selected for one environment, not of the
// seam. A zero-account dev surface asks the selected verifier for one through
// this interface instead of hardcoding a provider's token shape, and refuses
// loudly when the selected adapter does not offer it.
type SyntheticSessionMinter interface {
	MintSession(userSubject, orgSubject, role string) (string, error)
}

// Navigator is the identity provider's own page layout, and the only place
// that layout is known. A caller names a destination and where to come back
// to; the adapter owns every path segment, the name of the return parameter,
// and the escaping.
//
// Explicit methods rather than one method over a closed destination enum.
// package ui normalizes an unrecognised enum member to a defined default,
// which is right for a visual modifier — a badge rendering its neutral
// treatment is obviously wrong and harmless. There is no safe default
// destination: normalizing would send a visitor to a page nobody asked for,
// which is the defect class this port exists to remove. Explicit methods also
// make adding a destination a compile error in every adapter rather than a
// switch arm one of them forgets.
//
// Every method returns an error so an adapter with no page for a destination
// fails where the caller can see it. returnTo is where the provider should
// send the visitor back to; an empty returnTo means "no particular place".
type Navigator interface {
	// LoginURL is the provider's sign-in page.
	LoginURL(returnTo string) (string, error)
	// SignupURL is the provider's sign-up page.
	SignupURL(returnTo string) (string, error)
	// LogoutURL is the provider's sign-out page.
	LogoutURL(returnTo string) (string, error)
	// AccountURL is the provider's page for managing the signed-in account.
	AccountURL(returnTo string) (string, error)
	// OrganizationURL is the provider's page for managing the active
	// organization.
	OrganizationURL(returnTo string) (string, error)
	// CreateOrganizationURL is the provider's page for founding an
	// organization.
	CreateOrganizationURL(returnTo string) (string, error)
}

// Webhook is the whole provider-facing webhook contract: an adapter owns its
// signature header family and payload shape, and hands back one neutral
// event. No generic handler ever sees a provider header or payload field.
type Webhook interface {
	Verify(context.Context, []byte, http.Header) (Event, error)
}

type Event struct {
	ID, Provider, Type string
	User               *UserEvent
	Organization       *OrganizationEvent
	Membership         *MembershipEvent
}
type UserEvent struct{ Subject, Email, Name, AvatarURL string }
type OrganizationEvent struct{ Subject, Name, Slug, ImageURL string }
type MembershipEvent struct{ OrganizationSubject, UserSubject, Role string }
