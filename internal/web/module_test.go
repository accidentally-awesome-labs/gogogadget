package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/identity"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
	llmfake "github.com/gogogadget/gogogadget/internal/llm/fake"
	"github.com/gogogadget/gogogadget/internal/observability"
	ratelimitmemory "github.com/gogogadget/gogogadget/internal/ratelimit/memory"
	"github.com/gogogadget/gogogadget/internal/realtime"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The server module is what the runtime serves, so its constructor must produce
// a live handler wired to real collaborators. Everything else in the runtime is
// pointless if this returns nil.
func TestNewModuleProvidesServableHandler(t *testing.T) {
	pool, queries := testdb.Open(t, "web_module")
	host := apphost.Map(nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")

	module, err := NewModule(context.Background(), host, Deps{
		Config: &config.Config{Env: "test", AppURL: "http://localhost:8080"},
		DB:     pool, Queries: queries, Storage: storagefs.NewDevStore(t.TempDir()),
		Flags: flags.NewDBEvaluator(queries, 30*time.Second), Reporter: observability.NoopReporter{},
		Verifier: identity.FakeVerifier{}, Fetcher: identity.DevUserFetcher{},
		IdentityDeleter: identity.DevDeleter{}, IdentityNavigator: identity.LocalNavigator{},
		IdentityWebhook: identity.DevWebhook{}, Billing: &billing.MockClient{},
		BillingCatalog: billing.DefaultPlanCatalog(), BillingWebhook: billinglocal.LocalWebhook{},
		Analytics: analytics.NoopCapturer{}, LLM: llmfake.Completer{}, Realtime: realtime.NewMemory(), RateLimiter: ratelimitmemory.New(100, 200),
		SessionLoader: &identitysession.SessionLoader{Pool: pool, Verify: identity.FakeVerifier{}, Fetch: identity.DevUserFetcher{}},
	})
	if err != nil {
		t.Fatalf("NewModule: %v", err)
	}
	if module.Handler == nil {
		t.Fatal("Handler = nil; the runtime would serve nothing")
	}

	// A probe is the cheapest proof the chain is really assembled.
	recorder := httptest.NewRecorder()
	module.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", recorder.Code, http.StatusOK)
	}
}

// The database is not optional for the HTTP surface: every route that matters
// reads it, so booting without one would only produce errors under traffic.
func TestNewModuleRejectsMissingDependencies(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "v-test")
	cases := map[string]Deps{
		"no config":  {},
		"no pool":    {Config: &config.Config{}},
		"no queries": {Config: &config.Config{}, DB: &pgxpool.Pool{}},
	}
	for name, deps := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := NewModule(context.Background(), host, deps); err == nil {
				t.Fatalf("NewModule(%s) = nil error, want failure", name)
			}
		})
	}
}

func TestNewModuleRejectsMissingCapabilityWithoutDatabase(t *testing.T) {
	host := apphost.Map(nil, time.Now(), "v-test")
	base := Deps{
		Config:  &config.Config{Env: "test", AppURL: "http://localhost:8080"},
		DB:      &pgxpool.Pool{},
		Queries: &sqlc.Queries{},
	}
	for _, tc := range []struct {
		name string
		deps func() Deps
		want string
	}{
		{"storage", func() Deps {
			return base
		}, "server: storage store capability is required"},
		{"flags", func() Deps {
			d := base
			d.Storage = storagefs.NewDevStore(t.TempDir())
			return d
		}, "server: flags evaluator capability is required"},
		{"reporter", func() Deps {
			d := base
			d.Storage = storagefs.NewDevStore(t.TempDir())
			d.Flags = flags.NewDBEvaluator(base.Queries, time.Second)
			return d
		}, "server: observability reporter capability is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewModule(context.Background(), host, tc.deps())
			if err == nil || err.Error() != tc.want {
				t.Fatalf("NewModule error = %v, want %q", err, tc.want)
			}
		})
	}
}

// A bad content-type declaration is a wiring bug. It must refuse at boot, the
// same way configuration validation does, rather than surfacing as a 500 later.
func TestNewModuleRefusesInvalidContentTypes(t *testing.T) {
	pool, queries := testdb.Open(t, "web_module_types")
	host := apphost.Map(nil, time.Now(), "v-test")

	_, err := NewModule(context.Background(), host, Deps{
		Config:       &config.Config{Env: "test"},
		DB:           pool,
		Queries:      queries,
		ContentTypes: []content.Type{{}},
	})
	if err == nil {
		t.Fatal("NewModule(invalid content types) = nil error, want refusal")
	}
}
