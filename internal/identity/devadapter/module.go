// Package dev implements the deterministic, zero-account identity adapter.
package identitydev

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/identity"
)

type Deps struct{ Config *config.Config }

type Module struct {
	Verifier  identity.Verifier
	Fetcher   identity.UserFetcher
	Deleter   identity.Deleter
	Navigator identity.Navigator
	Webhook   identity.Webhook
}

func NewModule(ctx context.Context, _ apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("identity dev: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Module{
		Verifier:  identity.FakeVerifier{},
		Fetcher:   identity.DevUserFetcher{},
		Deleter:   identity.DevDeleter{},
		Navigator: identity.LocalNavigator{BaseURL: d.Config.AppURL},
		Webhook:   identity.DevWebhook{},
	}, nil
}

var _ identity.SubjectVerifier = identity.FakeVerifier{}
