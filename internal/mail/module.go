// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls; it is the one place that decides which mail
// adapter this deployment runs.
package mail

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

// Module is the constructed mail closure.
type Module struct {
	Sender Sender
}

// NewModule selects Resend when an API key is configured and the dev sender
// (log + tmp/emails) otherwise, so a fresh clone delivers mail to disk without
// a provider account.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	if d.Config == nil {
		return nil, fmt.Errorf("mail: config dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	log := h.Log()
	if d.Config.ResendConfigured() {
		log.Info("mail: resend", "from", d.Config.EmailFrom)
		return &Module{Sender: NewResendSender(d.Config.ResendAPIKey, d.Config.EmailFrom)}, nil
	}
	log.Info("mail: dev sender (tmp/emails)")
	return &Module{Sender: NewDevSender(log, "tmp/emails")}, nil
}
