// Package clerk implements the hosted Clerk identity adapter.
package clerk

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

type Deps struct{ Config *config.Config }

type navigator struct{ BaseURL string }

func (n navigator) LoginURL(returnTo string) string {
	return n.BaseURL + "/sign-in?redirect_url=" + returnTo
}
func (n navigator) SignupURL(returnTo string) string {
	return n.BaseURL + "/sign-up?redirect_url=" + returnTo
}
func (n navigator) AccountURL() string { return n.BaseURL }

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
		Verifier:  identity.NewClerkVerifier(d.Config.ClerkSecretKey),
		Fetcher:   identity.NewClerkUserFetcher(d.Config.ClerkSecretKey),
		Deleter:   identity.NewClerkDeleter(d.Config.ClerkSecretKey),
		Navigator: navigator{BaseURL: d.Config.ClerkPortalURL},
		Webhook:   identity.ClerkWebhook{Secret: d.Config.ClerkWebhookSecret},
	}, nil
}

var _ identity.SubjectVerifier = (*identity.ClerkVerifier)(nil)
