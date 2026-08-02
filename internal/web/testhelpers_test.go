package web

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/db/testdb"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/jackc/pgx/v5/pgxpool"
	svix "github.com/svix/svix-webhooks/go"
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
		Env:                 "test",
		AppURL:              "http://localhost:18080",
		ClerkPortalURL:      "https://accounts.example.test",
		ClerkFrontendAPIURL: "https://*.clerk.accounts.dev",
		ClerkWebhookSecret:  testWebhookSecret,
		DevAuthBypass:       true,
	}
	deps := Deps{
		Config: cfg, Log: testLogger(), DB: pool, Queries: sqlc.New(pool), Version: "test",
		Blog: &content.Blog{}, Docs: &content.Docs{},
		Verifier: identity.FakeVerifier{},
		Fetcher:  identity.DevUserFetcher{},
	}
	if mutate != nil {
		mutate(&deps)
	}
	return NewServer(deps)
}

// signSvix emits the exact headers a Clerk (Svix) delivery carries.
func signSvix(t *testing.T, secret, msgID string, payload []byte) http.Header {
	t.Helper()
	wh, err := svix.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := wh.Sign(msgID, time.Now(), payload)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("svix-id", msgID)
	h.Set("svix-timestamp", fmt.Sprint(time.Now().Unix()))
	h.Set("svix-signature", sig)
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
