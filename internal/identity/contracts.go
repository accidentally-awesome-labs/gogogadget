package identity

import (
	"context"
	"errors"
	"net/http"
)

var ErrLinkRequired = errors.New("identity: explicit account link required")

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
