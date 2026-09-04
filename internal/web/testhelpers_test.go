package web

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/flags"
	identitydev "github.com/gogogadget/gogogadget/internal/identity/devadapter"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
	"github.com/gogogadget/gogogadget/internal/observability"
	ratelimitmemory "github.com/gogogadget/gogogadget/internal/ratelimit/memory"
	"github.com/gogogadget/gogogadget/internal/realtime"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testWebhookSecret is the fixture secret for webhook test suites.
var testWebhookSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("gogogadget-test-secret-32b!"))

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// integrationPool opens the web package's own test database.
func integrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, _ := testdb.Open(t, "web")
	return pool
}

// integrationServer builds a Server against real Postgres with the
// FakeVerifier (DEV_AUTH_BYPASS) auth path.
func integrationServer(t *testing.T, mutate func(*Deps)) *Server {
	t.Helper()
	pool := integrationPool(t)
	cfg := config.Config{
		Env:    "test",
		AppURL: "http://localhost:18080",
		// Adapter-owned keys reach a consumer that does not declare them
		// through Values, exactly as the generated parse fills it. Setting the
		// typed field here instead would test a read no module is allowed to
		// make.
		Values: map[string]string{
			"CLERK_PORTAL_URL":       "https://accounts.example.test",
			"CLERK_FRONTEND_API_URL": "https://*.clerk.accounts.dev",
			"DEV_AUTH_BYPASS":        "true",
		},
		ClerkWebhookSecret: testWebhookSecret,
	}
	deps := Deps{
		Config: &cfg, Log: testLogger(), DB: pool, Queries: sqlc.New(pool), Version: "test",
		Docs: &content.Docs{}, Verifier: identitydev.Verifier{}, Fetcher: identitydev.UserFetcher{},
		IdentityDeleter: identitydev.Deleter{}, IdentityNavigator: identitydev.Navigator{BaseURL: cfg.AppURL},
		IdentityWebhook: identitydev.Webhook{}, BillingWebhook: billinglocal.LocalWebhook{},
		Billing: &billing.MockClient{}, BillingCatalog: billing.DefaultPlanCatalog(),
		Storage: storagefs.NewDevStore(t.TempDir()), Flags: flags.NewDBEvaluator(sqlc.New(pool), 30*time.Second), Reporter: observability.NoopReporter{},
		Analytics: analytics.NoopCapturer{}, LLM: unavailableCompleter{}, Realtime: realtime.NewMemory(), RateLimiter: ratelimitmemory.New(100, 200),
		SessionLoader: identitysession.Loader(&identitysession.SessionLoader{Pool: pool, Verify: identitydev.Verifier{}, Fetch: identitydev.UserFetcher{}, AdminEmail: cfg.AdminEmail}),
	}
	if mutate != nil {
		mutate(&deps)
	}
	if loader, ok := deps.SessionLoader.(*identitysession.SessionLoader); ok {
		loader.AdminEmail = deps.Config.AdminEmail
	}
	server, err := NewServer(deps)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

// seedEntries inserts content rows, invalidates the CMS cache so the very
// next public request sees them, and removes them at the end of the test.
// The web package shares one database across tests, so every caller must use
// slugs of its own.
func seedEntries(t *testing.T, s *Server, rows ...sqlc.CreateEntryParams) []sqlc.ContentEntry {
	t.Helper()
	out := make([]sqlc.ContentEntry, 0, len(rows))
	for _, row := range rows {
		if row.Meta == nil {
			row.Meta = []byte("{}")
		}
		entry, err := s.q.CreateEntry(t.Context(), row)
		if err != nil {
			t.Fatalf("seed content entry %s/%s: %v", row.Kind, row.Slug, err)
		}
		out = append(out, entry)
		t.Cleanup(func() {
			_ = s.q.DeleteEntry(context.Background(), entry.ID)
			s.cms.Invalidate()
		})
	}
	s.cms.Invalidate()
	return out
}

// publishedAt is the timestamp shorthand seeded entries use.
func publishedAt(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// identityDelivery is the header set the selected identity adapter's webhook
// reads for its delivery id. The receiver is provider-neutral, so its
// fixtures come from the adapter the test harness selects — the dev adapter's
// unsigned envelope — never from a hosted provider's signature scheme.
func identityDelivery(msgID string) http.Header {
	h := http.Header{}
	h.Set("id", msgID)
	return h
}

// sessionCookie builds a synthetic e2e: session cookie.
func sessionCookie(userID, orgID, role string) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: "e2e:" + userID + ":" + orgID + ":" + role}
}

// serve issues a request against the full middleware stack.
func serve(t *testing.T, s *Server, method, target string, body []byte, headers http.Header, cookies ...*http.Cookie) (int, http.Header, string) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = strings.NewReader(string(body))
	}
	req := httptest.NewRequest(method, target, rdr)
	if method != http.MethodGet && req.Header.Get("Origin") == "" {
		// Browsers always send Origin on mutating requests; nosurf v1.2
		// enforces same-origin via Sec-Fetch-Site/Origin/Referer.
		req.Header.Set("Origin", "http://"+req.Host)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Header(), rec.Body.String()
}
