// Package-level module wiring. Deps/NewModule is the uniform constructor shape
// the generated bootstrap calls. The worker collaborates with six systems, and
// wiring it by assigning exported fields after construction is how one of them
// gets silently forgotten — so they arrive as typed dependencies instead.
package jobs

import (
	"context"
	"fmt"
	"sync"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/mail"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage"
)

// Deps is the typed dependency set the generated bootstrap supplies. Billing and
// Storage are optional: unconfigured billing makes the usage flush no-op, and an
// absent store makes export jobs fail loudly rather than silently succeed.
type Deps struct {
	Config   *config.Config
	Queries  *sqlc.Queries
	Sender   mail.Sender
	Billing  billing.Client
	Storage  storage.Store
	Reporter observability.Reporter
}

// Module is the constructed background-worker closure.
type Module struct {
	Worker *Worker

	startOnce sync.Once
	stopOnce  sync.Once
	cancel    context.CancelFunc
	done      chan struct{}
}

// NewModule builds the worker with every collaborator it needs.
func NewModule(ctx context.Context, h apphost.Host, d Deps) (*Module, error) {
	switch {
	case d.Config == nil:
		return nil, fmt.Errorf("jobs: config dependency is required")
	case d.Queries == nil:
		return nil, fmt.Errorf("jobs: queries dependency is required")
	case d.Sender == nil:
		return nil, fmt.Errorf("jobs: mail sender dependency is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	worker := NewWorkerWithEnvironment(d.Queries, d.Sender, h.Log(), d.Config.Env)
	worker.Billing = d.Billing
	worker.Storage = d.Storage
	worker.AppURL = d.Config.AppURL
	worker.AuditRetentionDays = d.Config.AuditRetentionDays

	// A dead-lettered job is the one failure nobody is watching for, so it is
	// always reported: the no-op reporter keeps this a single unconditional path.
	reporter := d.Reporter
	if reporter == nil {
		reporter = observability.NoopReporter{}
	}
	worker.OnDeadLetter = func(kind string, err error) {
		reporter.Capture(fmt.Errorf("job %s dead-lettered: %w", kind, err))
	}

	return &Module{Worker: worker, done: make(chan struct{})}, nil
}

// Start launches the claim loop and returns. The runtime starts background
// services and then hands control to whatever serves traffic, so a Start that
// blocked would stop the process from ever listening.
func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.Worker == nil {
		return fmt.Errorf("jobs: module is not constructed")
	}
	m.startOnce.Do(func() {
		loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		m.cancel = cancel
		go func() {
			defer close(m.done)
			m.Worker.Run(loopCtx)
		}()
	})
	return nil
}

// Stop cancels the claim loop and waits for the in-flight job to finish. It is
// idempotent, and it honors ctx so one stuck handler cannot block shutdown.
func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.Worker == nil {
		return nil
	}
	m.stopOnce.Do(func() {
		if m.cancel != nil {
			m.cancel()
		} else {
			// Stopped before ever starting: nothing will close done.
			close(m.done)
		}
	})
	select {
	case <-m.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
