// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls; it is the one place that decides which
// identity ports this deployment runs.
package identity

import (
	"context"
	"fmt"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
)

// Deps is the typed dependency set the generated bootstrap supplies.
type Deps struct {
	Config *config.Config
}

// Module is the constructed identity closure. All three ports move together;
// all three are nil when nothing is configured, which makes /app answer 503
// rather than trust a synthetic user.
type Module struct {
	Verifier Verifier
	Fetcher  UserFetcher
	Deleter  Deleter
	Navigator Navigator
	Webhook Webhook
}

// NewModule resolves the identity triple by a single precedence rule: the dev
// bypass wins (the e2e suite sets it alongside a real key), then Clerk, then
// nothing.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("identity: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log := h.Log()
	switch {
	case d.Config.DevAuthBypass:
		log.Warn("DEV_AUTH_BYPASS enabled — synthetic e2e: tokens accepted")
		return &Module{
			Verifier: FakeVerifier{},
			Fetcher:  DevUserFetcher{},
			Deleter:  DevDeleter{},
		}, nil
	case d.Config.ClerkConfigured():
		return &Module{
			Verifier: NewClerkVerifier(d.Config.ClerkSecretKey),
			Fetcher:  NewClerkUserFetcher(d.Config.ClerkSecretKey),
			Deleter:  NewClerkDeleter(d.Config.ClerkSecretKey),
		}, nil
	default:
		log.Warn("clerk not configured — /app routes will 503")
		return &Module{}, nil
	}
}
