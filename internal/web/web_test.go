package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testServer(t *testing.T, mutate func(*config.Config)) *Server {
	t.Helper()
	cfg := config.Config{
		Env:                 "development",
		AppURL:              "http://localhost:8080",
		ClerkFrontendAPIURL: "https://clerk.example.com",
	}
	if mutate != nil {
		mutate(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(cfg, log, nil, "test", &content.Blog{}, &content.Docs{})
}

func TestSecureHeadersCSP(t *testing.T) {
	s := testServer(t, nil)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, csp)
	assert.Contains(t, csp, "default-src 'self'")
	assert.Contains(t, csp, "script-src 'self'")
	assert.Contains(t, csp, "connect-src 'self' https://clerk.example.com")
	assert.Contains(t, csp, "frame-ancestors 'none'")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	// HSTS is production-only.
	assert.Empty(t, rec.Header().Get("Strict-Transport-Security"))
}

func TestSecureHeadersHSTSProduction(t *testing.T) {
	s := testServer(t, func(c *config.Config) { c.Env = "production" })
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, "max-age=63072000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
}

func TestCSRFForbidden(t *testing.T) {
	s := testServer(t, nil)
	req := httptest.NewRequest("POST", "/anything", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCSRFExemptPaths(t *testing.T) {
	s := testServer(t, nil)
	for _, path := range []string{"/webhooks/clerk", "/api/v1/projects", "/ingest/e/", "/healthz"} {
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusForbidden, rec.Code, "path %s must be CSRF-exempt", path)
	}
}

func TestCSRFCookieNames(t *testing.T) {
	assert.Equal(t, "__Host-csrf", csrfCookieName(true))
	assert.Equal(t, "csrf_token", csrfCookieName(false))
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:54321"
	assert.Equal(t, "10.0.0.1", clientIP(req))
	req.Header.Set("Fly-Client-IP", "203.0.113.9")
	assert.Equal(t, "203.0.113.9", clientIP(req))
}

func TestRateLimit429(t *testing.T) {
	s := testServer(t, nil)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	var got429 bool
	for i := 0; i < 210; i++ {
		resp, err := http.Get(srv.URL + "/pricing")
		require.NoError(t, err)
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			assert.Equal(t, "1", resp.Header.Get("Retry-After"))
		}
	}
	assert.True(t, got429, "210 rapid requests (burst 200) must produce 429s")
}

func TestRenderBoostedVsFragment(t *testing.T) {
	s := testServer(t, nil)
	content := templates.NotFound()

	// Plain request → full layout.
	req := httptest.NewRequest("GET", "/nope", nil)
	rec := httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.Contains(t, rec.Body.String(), `<html lang="en">`)

	// HX-Request + HX-Boosted (boosted nav) → STILL the full layout.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Boosted", "true")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.Contains(t, rec.Body.String(), `<html lang="en">`, "boosted nav must get the full layout")

	// HX-Request alone → bare fragment.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.NotContains(t, rec.Body.String(), `<html lang="en">`, "plain HX request must get a fragment")
	assert.Contains(t, rec.Body.String(), "404")
}

func TestRedirect(t *testing.T) {
	s := testServer(t, nil)
	_ = s

	// Plain: 303.
	req := httptest.NewRequest("POST", "/x", nil)
	rec := httptest.NewRecorder()
	Redirect(rec, req, "/target")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/target", rec.Header().Get("Location"))

	// HX: HX-Redirect header instead of a 30x.
	req = httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	Redirect(rec, req, "/target")
	assert.Equal(t, "/target", rec.Header().Get("HX-Redirect"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestToast(t *testing.T) {
	rec := httptest.NewRecorder()
	Toast(rec, "success", "Saved")
	assert.JSONEq(t, `{"toast":{"type":"success","message":"Saved"}}`, rec.Header().Get("HX-Trigger"))
}

func TestNotFoundStyled(t *testing.T) {
	s := testServer(t, nil)
	req := httptest.NewRequest("GET", "/definitely-not-a-page", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "404")
	assert.Contains(t, rec.Body.String(), `<html lang="en">`)
}
