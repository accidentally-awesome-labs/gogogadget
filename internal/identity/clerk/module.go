// Package clerk implements the hosted Clerk identity adapter. It is the only
// package in the tree that imports the Clerk SDK or the svix verification
// library; the identity seam holds contracts alone.
package clerk

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Provider is the value stamped on every claim and event this adapter
// produces. It is the provider key the identity mapping tables store.
const Provider = "clerk"

type Deps struct{ Config *config.Config }

// Navigator builds Clerk Account Portal URLs.
type Navigator struct{ BaseURL string }

func (n Navigator) LoginURL(returnTo string) string {
	return n.BaseURL + "/sign-in?redirect_url=" + returnTo
}
func (n Navigator) SignupURL(returnTo string) string {
	return n.BaseURL + "/sign-up?redirect_url=" + returnTo
}
func (n Navigator) AccountURL() string { return n.BaseURL }

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
