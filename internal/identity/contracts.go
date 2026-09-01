package identity

import (
	"context"
	"errors"
	"net/http"
)

var ErrLinkRequired = errors.New("identity: explicit account link required")

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

type Navigator interface {
	LoginURL(returnTo string) string
	SignupURL(returnTo string) string
	AccountURL() string
}

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

// LocalNavigator is useful for development and deterministic tests.
type LocalNavigator struct{ BaseURL string }

func (n LocalNavigator) LoginURL(returnTo string) string {
	return n.BaseURL + "/login?return_to=" + returnTo
}
func (n LocalNavigator) SignupURL(returnTo string) string {
	return n.BaseURL + "/signup?return_to=" + returnTo
}
func (n LocalNavigator) AccountURL() string { return n.BaseURL + "/account" }
