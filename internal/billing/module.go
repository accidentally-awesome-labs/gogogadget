// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls.
package billing

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

// Module is the constructed billing closure. Client is nil when unconfigured:
// there is no local stand-in for a payment provider, so billing routes 503.
type Module struct {
	Client Client
}

// NewModule installs the configured product IDs into plan truth, then selects
// the Polar client when an access token is configured.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("billing: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	SetPolarProductIDs(d.Config.PolarProductPro, d.Config.PolarProductTeam)

	log := h.Log()
	if !d.Config.PolarConfigured() {
		log.Warn("polar not configured — billing routes will 503")
		return &Module{}, nil
	}
	log.Info("billing: polar", "server", d.Config.PolarServer)
	return &Module{Client: NewPolarClient(d.Config.PolarAccessToken, d.Config.PolarServer)}, nil
}
