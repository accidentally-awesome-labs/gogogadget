package identityhosted

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Provider is what this adapter stamps on every claim and event it returns.
const Provider = "fixture-hosted"

type Deps struct {
	Config *config.Config
}

type Module struct {
	Deleter   identity.Deleter
	Fetcher   identity.UserFetcher
	Navigator identity.Navigator
	Verifier  identity.Verifier
	Webhook   identity.Webhook
}

type verifier struct{}

func (verifier) Verify(_ context.Context, token string) (*identity.ProviderClaims, error) {
	const prefix = "hosted:"
	if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
		return nil, identity.ErrInvalidToken
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: token[len(prefix):]}, nil
}

type fetcher struct{}

func (fetcher) Fetch(_ context.Context, subject string) (identity.UserProfile, error) {
	return identity.UserProfile{Email: subject + "@hosted.invalid", Name: subject}, nil
}

type deleter struct{}

func (deleter) DeleteUser(ctx context.Context, _ string) error { return ctx.Err() }

type navigator struct{ appURL string }

func (n navigator) LoginURL(returnTo string) string {
	return n.appURL + "/sign-in?redirect_url=" + returnTo
}
func (n navigator) SignupURL(returnTo string) string {
	return n.appURL + "/sign-up?redirect_url=" + returnTo
}
func (n navigator) AccountURL() string { return n.appURL + "/account" }

// webhook reads this adapter's own header family and envelope, so no generic
// handler ever learns a provider's signature scheme.
type webhook struct{}

func (webhook) Verify(_ context.Context, body []byte, headers http.Header) (identity.Event, error) {
	id := headers.Get("hosted-delivery-id")
	if id == "" {
		return identity.Event{}, fmt.Errorf("identity hosted fixture: missing delivery id")
	}
	var envelope struct {
		Type string `json:"type"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return identity.Event{}, fmt.Errorf("identity hosted fixture: %w", err)
	}
	event := identity.Event{ID: id, Provider: Provider, Type: envelope.Type}
	if envelope.Data.ID != "" {
		event.User = &identity.UserEvent{Subject: envelope.Data.ID}
	}
	return event, nil
}

func NewModule(ctx context.Context, _ apphost.Host, deps Deps) (*Module, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("identity hosted fixture: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{
		Deleter:   deleter{},
		Fetcher:   fetcher{},
		Navigator: navigator{appURL: deps.Config.AppURL},
		Verifier:  verifier{},
		Webhook:   webhook{},
	}, nil
}

func (*Module) Health(ctx context.Context) error { return ctx.Err() }

var (
	_ identity.Deleter      = deleter{}
	_ identity.UserFetcher  = fetcher{}
	_ identity.Navigator    = navigator{}
	_ identity.Verifier     = verifier{}
	_ identity.Webhook      = webhook{}
	_ apphost.HealthChecker = (*Module)(nil)
)
