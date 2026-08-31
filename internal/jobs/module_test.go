package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	maildev "github.com/gogogadget/gogogadget/internal/mail/dev"
	"github.com/gogogadget/gogogadget/internal/observability"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
)

// The worker collaborates with six systems. Wiring it by assigning exported
// fields after construction is how a collaborator gets silently forgotten, so
// the module constructor takes them as typed dependencies and the test pins that
// every one of them lands.
func TestNewModuleWiresWorkerCollaborators(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	store := storagefs.NewDevStore(t.TempDir())
	sender := maildev.NewDevSender(host.Log(), t.TempDir())

	module, err := NewModule(context.Background(), host, Deps{
		Config:   &config.Config{AppURL: "https://example.test", AuditRetentionDays: 42},
		Queries:  &sqlc.Queries{},
		Sender:   sender,
		Storage:  store,
		Reporter: observability.NoopReporter{},
	})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if module.Worker == nil {
		t.Fatal("Worker = nil")
	}
	if got := module.Worker.AppURL; got != "https://example.test" {
		t.Fatalf("AppURL = %q, want the configured base URL", got)
	}
	if got := module.Worker.AuditRetentionDays; got != 42 {
		t.Fatalf("AuditRetentionDays = %d, want 42", got)
	}
	if module.Worker.Storage != store {
		t.Fatal("Storage collaborator was not wired")
	}
	if module.Worker.OnDeadLetter == nil {
		t.Fatal("OnDeadLetter was not wired; dead letters would be invisible")
	}
}

// Billing is genuinely optional: unconfigured means usage flush no-ops and
// events stay local, which is a shipped degraded mode rather than a failure.
func TestNewModuleAcceptsAbsentBilling(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	module, err := NewModule(context.Background(), host, Deps{
		Config:  &config.Config{},
		Queries: &sqlc.Queries{},
		Sender:  maildev.NewDevSender(host.Log(), t.TempDir()),
		Storage: storagefs.NewDevStore(t.TempDir()),
	})
	if err != nil {
		t.Fatalf("NewModule(no billing): %v", err)
	}
	if module.Worker.Billing != (billing.Client)(nil) {
		t.Fatalf("Billing = %T, want nil", module.Worker.Billing)
	}
}

// The queue is the whole point of the module: without queries it cannot claim a
// row, so booting it would produce a worker that silently does nothing.
func TestNewModuleRejectsMissingRequiredDependencies(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	cases := map[string]Deps{
		"no config":  {Queries: &sqlc.Queries{}, Sender: maildev.NewDevSender(host.Log(), t.TempDir())},
		"no queries": {Config: &config.Config{}, Sender: maildev.NewDevSender(host.Log(), t.TempDir())},
		"no sender":  {Config: &config.Config{}, Queries: &sqlc.Queries{}},
	}
	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewModule(context.Background(), host, deps); err == nil {
				t.Fatalf("NewModule(%s) = nil error, want failure", name)
			}
		})
	}
}

// Start must return promptly: the runtime starts background services and then
// hands control back to whatever serves traffic. Stop must end the loop.
func TestModuleStartReturnsAndStopEndsTheLoop(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "test")
	module, err := NewModule(context.Background(), host, Deps{
		Config:  &config.Config{},
		Queries: &sqlc.Queries{},
		Sender:  maildev.NewDevSender(host.Log(), t.TempDir()),
	})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}

	started := make(chan error, 1)
	go func() { started <- module.Start(context.Background()) }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start blocked; it must launch the loop and return")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := module.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// Idempotent: shutdown paths can reach it more than once.
	if err := module.Stop(ctx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}
