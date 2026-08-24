package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The server module is what the runtime serves, so its constructor must produce
// a live handler wired to real collaborators. Everything else in the runtime is
// pointless if this returns nil.
func TestNewModuleProvidesServableHandler(t *testing.T) {
	pool, queries := testdb.Open(t, "web_module")
	host := apphost.Map(nil, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "v-test")

	module, err := NewModule(context.Background(), host, Deps{
		Config:  &config.Config{Env: "test", AppURL: "http://localhost:8080"},
		DB:      pool,
		Queries: queries,
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
