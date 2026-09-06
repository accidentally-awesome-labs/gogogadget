package identitylocal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Provider is what this adapter stamps on every claim and event it returns, so
// identity_subjects rows stay attributable after a swap.
const Provider = "fixture-local"

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

// verifier accepts "fixture:<subject>" tokens, which is the zero-account shape
// this fixture exists to stand in for.
type verifier struct{}

func (verifier) Verify(_ context.Context, token string) (*identity.ProviderClaims, error) {
	if len(token) <= len("fixture:") || token[:len("fixture:")] != "fixture:" {
		return nil, identity.ErrInvalidToken
	}
	return &identity.ProviderClaims{Provider: Provider, UserSubject: token[len("fixture:"):]}, nil
}

type fetcher struct{}

func (fetcher) Fetch(_ context.Context, subject string) (identity.UserProfile, error) {
	return identity.UserProfile{Email: subject + "@example.invalid", Name: subject}, nil
}

type deleter struct{}

func (deleter) DeleteUser(ctx context.Context, _ string) error { return ctx.Err() }

// navigator answers this fixture's own local surface. It refuses the three
// destinations a zero-account adapter has nothing to point at, which is the
// contract's other legal answer.
type navigator struct{}

func (navigator) LoginURL(returnTo string) (string, error) {
	return devLogin(returnTo)
}
func (navigator) SignupURL(returnTo string) (string, error) {
	return devLogin(returnTo)
}
func (navigator) LogoutURL(string) (string, error) { return "/", nil }
func (navigator) AccountURL(string) (string, error) {
	return "", fmt.Errorf("identity fixture-local: no account page: %w", identity.ErrNoDestination)
}
func (navigator) OrganizationURL(string) (string, error) {
	return "", fmt.Errorf("identity fixture-local: no organization page: %w", identity.ErrNoDestination)
}
func (navigator) CreateOrganizationURL(string) (string, error) {
	return "", fmt.Errorf("identity fixture-local: no org-creation page: %w", identity.ErrNoDestination)
}

func devLogin(returnTo string) (string, error) {
	if returnTo == "" {
		return "/dev/login", nil
	}
	return "/dev/login?return_to=" + url.QueryEscape(returnTo), nil
}

// webhook reads this adapter's own flat envelope. It never parses another
// provider's payload shape, which is what keeps the seam neutral.
type webhook struct{}

func (webhook) Verify(_ context.Context, body []byte, headers http.Header) (identity.Event, error) {
	id := headers.Get("id")
	if id == "" {
		return identity.Event{}, fmt.Errorf("identity local fixture: missing delivery id")
	}
	var envelope struct {
		Type string `json:"type"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return identity.Event{}, fmt.Errorf("identity local fixture: %w", err)
	}
	event := identity.Event{ID: id, Provider: Provider, Type: envelope.Type}
	if envelope.Data.ID != "" {
		event.User = &identity.UserEvent{Subject: envelope.Data.ID}
	}
	return event, nil
}

func NewModule(ctx context.Context, _ apphost.Host, deps Deps) (*Module, error) {
	if deps.Config == nil {
		return nil, fmt.Errorf("identity local fixture: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{
		Deleter:   deleter{},
		Fetcher:   fetcher{},
		Navigator: navigator{},
		Verifier:  verifier{},
		Webhook:   webhook{},
	}, nil
}

var (
	_ identity.Deleter     = deleter{}
	_ identity.UserFetcher = fetcher{}
	_ identity.Navigator   = navigator{}
	_ identity.Verifier    = verifier{}
	_ identity.Webhook     = webhook{}
)
