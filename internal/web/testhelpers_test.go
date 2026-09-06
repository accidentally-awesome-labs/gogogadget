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
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/identity"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/ratelimit"
	"github.com/gogogadget/gogogadget/internal/realtime"
	"github.com/gogogadget/gogogadget/internal/storage"
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

// integrationServer builds a Server against real Postgres, authenticated by
// the identity seam's own doubles.
//
// Every capability here is either a seam-owned double or a seam-owned
// implementation, and none of them is an adapter. This harness serves ~12
// files owned by eight different modules, so an adapter constructed here
// would pin one provider selection into all of them — and `requires` cannot
// express "providers.identity.test is identity-dev", so the pin would be
// undeclarable as well as undeclared.
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
			"CLERK_WEBHOOK_SECRET":   testWebhookSecret,
		},
	}
	deps := Deps{
		Config: &cfg, Log: testLogger(), DB: pool, Queries: sqlc.New(pool), Version: "test",
		Docs: &content.Docs{}, Verifier: identity.MockVerifier{}, Fetcher: identity.MockUserFetcher{},
		// The navigator answers: its URLs are rendered into pages these
		// suites assert on. identity.MockNavigator's zero value refuses
		// every destination instead, which is what the refusal suites use.
		IdentityDeleter: &identity.MockDeleter{}, IdentityNavigator: identity.MockNavigator{BaseURL: cfg.AppURL},
		IdentityWebhook: identity.MockWebhook{}, BillingWebhook: billing.MockWebhook{},
		Billing: &billing.MockClient{}, BillingCatalog: billing.DefaultPlanCatalog(),
		Storage: storage.NewMockStore(), Flags: flags.NewDBEvaluator(sqlc.New(pool), 30*time.Second), Reporter: observability.NoopReporter{},
		Analytics: analytics.NoopCapturer{}, LLM: unavailableCompleter{}, Realtime: realtime.NewMemory(), RateLimiter: ratelimit.NewMockLimiter(100, 200),
		SessionLoader: identitysession.Loader(&identitysession.SessionLoader{Pool: pool, Verify: identity.MockVerifier{}, Fetch: identity.MockUserFetcher{}, AdminEmail: cfg.AdminEmail}),
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

// identityDelivery encodes one neutral identity event as a delivery the
// selected identity webhook accepts. The receiver is provider-neutral, and
// so is this fixture: the seam's double owns the encoding, so no payload
// writes an adapter's wire format. Hand-written envelopes were the last
// coupling here that no import-based check could see, because a payload can
// inline an adapter's JSON while importing nothing.
func identityDelivery(deliveryID string, event identity.Event) ([]byte, http.Header) {
	payload, headers, err := identity.MockDelivery(deliveryID, event)
	if err != nil {
		panic(err)
	}
	return payload, headers
}

// sessionCookie mints a session cookie through the seam's
// SyntheticSessionMinter, the same port the zero-account dev surface uses.
// It deliberately does not spell a token grammar: writing a provider's token
// shape into neutral code is the defect internal/web/workflow_dev_session.go
// documents removing, and it survived here afterwards.
//
// A mint failure panics rather than failing a test: it means the harness is
// broken, and every one of the ~135 callers would report the same thing one
// frame further from the cause.
func sessionCookie(userID, orgID, role string) *http.Cookie {
	token, err := identity.MockVerifier{}.MintSession(userID, orgID, role)
	if err != nil {
		panic(err)
	}
	return &http.Cookie{Name: sessionCookieName, Value: token}
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
