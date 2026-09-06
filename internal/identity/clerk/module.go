// Package clerk implements the hosted Clerk identity adapter. It is the only
// package in the tree that imports the Clerk SDK or the svix verification
// library; the identity seam holds contracts alone.
package clerk

import (
	"context"
	"fmt"
	"net/url"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Provider is the value stamped on every claim and event this adapter
// produces. It is the provider key the identity mapping tables store.
const Provider = "clerk"

type Deps struct{ Config *config.Config }

// Navigator builds Clerk Account Portal URLs. Every path segment below is
// Clerk's, `redirect_url` is Clerk's parameter name, and the escaping is
// Clerk's problem — the neutral web package used to spell three of these and
// disagreed with itself about the last one.
type Navigator struct{ BaseURL string }

func (n Navigator) LoginURL(returnTo string) (string, error) {
	return n.page("/sign-in", returnTo)
}
func (n Navigator) SignupURL(returnTo string) (string, error) {
	return n.page("/sign-up", returnTo)
}
func (n Navigator) LogoutURL(returnTo string) (string, error) {
	return n.page("/sign-out", returnTo)
}
func (n Navigator) AccountURL(returnTo string) (string, error) {
	return n.page("/user", returnTo)
}
func (n Navigator) OrganizationURL(returnTo string) (string, error) {
	return n.page("/organization", returnTo)
}
func (n Navigator) CreateOrganizationURL(returnTo string) (string, error) {
	return n.page("/create-organization", returnTo)
}

// page is the one place the Account Portal's URL shape is written.
//
// An unconfigured base is a refusal rather than a relative URL.
// CLERK_PORTAL_URL is production-required, so an empty base means this
// adapter was constructed without the configuration it needs; concatenating
// a Clerk path onto "" yields a same-origin path this application does not
// serve, which is a 404 that looks like a working link.
func (n Navigator) page(path, returnTo string) (string, error) {
	if n.BaseURL == "" {
		return "", fmt.Errorf("identity clerk: CLERK_PORTAL_URL is required to reach %s: %w",
			path, identity.ErrNoDestination)
	}
	if returnTo == "" {
		return n.BaseURL + path, nil
	}
	return n.BaseURL + path + "?redirect_url=" + url.QueryEscape(returnTo), nil
}

type Module struct {
	Verifier  identity.Verifier
	Fetcher   identity.UserFetcher
	Deleter   identity.Deleter
	Navigator identity.Navigator
	Webhook   identity.Webhook
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("identity clerk: config dependency is required")
	}
	if d.Config.ClerkSecretKey == "" {
		return nil, fmt.Errorf("identity clerk: CLERK_SECRET_KEY is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{
		Verifier:  NewVerifier(d.Config.ClerkSecretKey),
		Fetcher:   NewUserFetcher(d.Config.ClerkSecretKey),
		Deleter:   NewDeleter(d.Config.ClerkSecretKey),
		Navigator: Navigator{BaseURL: d.Config.ClerkPortalURL},
		Webhook:   Webhook{Secret: d.Config.ClerkWebhookSecret},
	}, nil
}

var (
	_ identity.SubjectVerifier             = (*Verifier)(nil)
	_ identity.OrganizationSubjectVerifier = (*Verifier)(nil)
	_ identity.UserFetcher                 = (*UserFetcher)(nil)
	_ identity.Navigator                   = Navigator{}
)
