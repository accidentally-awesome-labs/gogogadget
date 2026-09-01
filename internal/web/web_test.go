package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/billinglocal"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/observability"
	storagefs "github.com/gogogadget/gogogadget/internal/storage/filesystem"
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
	server, err := NewServer(Deps{
		Config: &cfg, Log: log, Version: "test",
		Docs: &content.Docs{}, Storage: storagefs.NewDevStore(t.TempDir()),
		Flags: flags.NewDBEvaluator(nil, 30*time.Second), Reporter: observability.NoopReporter{},
		Verifier: identity.FakeVerifier{}, Fetcher: identity.DevUserFetcher{},
		IdentityDeleter: identity.DevDeleter{}, IdentityNavigator: identity.LocalNavigator{},
		IdentityWebhook: identity.DevWebhook{}, Billing: &billing.MockClient{},
		BillingCatalog: billing.DefaultPlanCatalog(), BillingWebhook: billinglocal.LocalWebhook{},
		SessionLoader: testSessionLoader{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

// renderToString renders a component outside a request, for asserting on the
// markup a template actually emits.
func renderToString(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, c.Render(t.Context(), &sb))
	return sb.String()
}

// chromeAround splits a rendered document into everything before and after the
// #content swap target — i.e. the layout chrome a navigation does NOT replace.
func chromeAround(t *testing.T, html string) (before, after string) {
	t.Helper()
	open := strings.Index(html, `<main id="content"`)
	require.GreaterOrEqual(t, open, 0, "layout must expose the #content swap target")
	close := strings.LastIndex(html, `</main>`)
	require.Greater(t, close, open)
	return html[:open], html[close+len(`</main>`):]
}

// Navigation between public and docs pages swaps ONLY #content, so the chrome
// around it has to be identical — otherwise whatever differs survives into a
// page that never asked for it (a docs table of contents stranded on /pricing)
// or goes missing from one that did (no footer). Given the same Page, the two
// layouts must therefore render byte-identical chrome, and everything
// layout-specific must live inside the swap target.
func TestPublicAndDocsLayoutsShareChrome(t *testing.T) {
	page := Page{
		Title:  "Same page",
		AppURL: "http://localhost:8080",
		Path:   "/docs/frontend",
		Docs: &content.Docs{Sections: []content.DocSection{{
			Name:  "Features",
			Pages: []content.DocPage{{Slug: "frontend", Title: "Frontend"}},
		}}},
	}
	body := templates.NotFound()

	pub := renderToString(t, templates.PublicLayout(page, body))
	docs := renderToString(t, templates.DocsLayout(page, body))

	pubBefore, pubAfter := chromeAround(t, pub)
	docsBefore, docsAfter := chromeAround(t, docs)
	assert.Equal(t, pubBefore, docsBefore, "chrome before #content must match")
	assert.Equal(t, pubAfter, docsAfter, "chrome after #content must match")

	// The docs table of contents is the thing that differs, so it must be inside
	// the swap target — never in the chrome.
	assert.Contains(t, docs, `aria-label="Documentation"`)
	assert.NotContains(t, docsBefore, `aria-label="Documentation"`)
	assert.NotContains(t, docsAfter, `aria-label="Documentation"`)
	assert.NotContains(t, pub, `aria-label="Documentation"`)

	// Both keep exactly one <main> and one footer.
	for name, html := range map[string]string{"public": pub, "docs": docs} {
		assert.Equal(t, 1, strings.Count(html, "<main "), name+" needs exactly one <main>")
		assert.Equal(t, 1, strings.Count(html, "<footer"), name+" needs a footer")
	}
}

// An in-page anchor must not be boosted: htmx would fetch the page, repaint
// #content at the top of the document and only then scroll to the fragment,
// flashing the wrong section. Unboosted, the browser just scrolls.
func TestAnchorNavLinksAreNotBoosted(t *testing.T) {
	nav := renderToString(t, templates.Nav(Page{Path: "/"}))
	require.Contains(t, nav, `href="/#features"`)

	features := nav[strings.Index(nav, `href="/#features"`):]
	features = features[:strings.Index(features, "</a>")]
	assert.NotContains(t, features, "hx-boost", "anchor links must not be boosted")
	assert.NotContains(t, features, "hx-target")

	pricing := nav[strings.Index(nav, `href="/pricing"`):]
	pricing = pricing[:strings.Index(pricing, "</a>")]
	assert.Contains(t, pricing, `hx-boost="true"`, "page links must stay boosted")
	assert.Contains(t, pricing, `hx-swap="`+NavSwap+`"`)
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

// CSRF exemptions are per route, not per path: each one is the policy a specific
// method+pattern declared. That is the point of moving off prefix regexes, so it
// is asserted both ways — the declared surfaces are exempt, and a method nobody
// declared does not inherit the exemption.
func TestCSRFExemptRoutes(t *testing.T) {
	s := testServer(t, nil)

	exempt := []struct{ method, path string }{
		{"POST", "/webhooks/clerk"},  // svix signature
		{"POST", "/webhooks/polar"},  // standard webhooks signature
		{"POST", "/api/v1/projects"}, // cookieless bearer transport
		{"POST", "/ingest/e/"},       // same-origin analytics proxy
		{"GET", "/healthz"},          // probe, no session
		{"GET", "/readyz"},           // probe, no session
	}
	for _, tc := range exempt {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusForbidden, rec.Code,
			"%s %s declares a CSRF exemption", tc.method, tc.path)
	}

	// /healthz is declared GET-only. A POST to it is not a declared route, so it
	// must not inherit the exemption — the old prefix/path form exempted every
	// method on the path, which is how an exemption widens past its reason.
	req := httptest.NewRequest("POST", "/healthz", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"POST /healthz is undeclared and must not be CSRF-exempt")
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

	// HX history cache misses re-fetch a complete history element.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.Contains(t, rec.Body.String(), `<html lang="en">`, "history restore must get the full layout")

	// HX-Request alone → bare fragment.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.NotContains(t, rec.Body.String(), `<html lang="en">`, "plain HX request must get a fragment")
	assert.Contains(t, rec.Body.String(), "404")

	// htmx 4 states intent outright. HX-Request-Type: full → complete page…
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "full")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.Contains(t, rec.Body.String(), `<html lang="en">`,
		`HX-Request-Type: full must get the full layout even without HX-Boosted`)

	// …and "partial" → fragment, boosted or not.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Target", "div#table-container")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.NotContains(t, rec.Body.String(), `<html lang="en">`)

	// Replacing the whole content box IS a navigation, whatever the type says.
	// htmx 4.0.0-beta6 labels an HX-Location payload's select as "partial", so
	// this is the case that keeps server-driven navigation rendering a page.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "partial")
	req.Header.Set("HX-Target", "main#content")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.Contains(t, rec.Body.String(), `<html lang="en">`,
		"a request that swaps #content outright must get the full layout")

	// A nested id ending in "content" must NOT be mistaken for the content box.
	req = httptest.NewRequest("GET", "/nope", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "div#subcontent")
	rec = httptest.NewRecorder()
	s.Render(rec, req, Page{Title: "x", Layout: templates.LayoutPublic}, content)
	assert.NotContains(t, rec.Body.String(), `<html lang="en">`)
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

func TestNavigate(t *testing.T) {
	// Plain clients still get a real redirect.
	req := httptest.NewRequest("POST", "/x", nil)
	rec := httptest.NewRecorder()
	Navigate(rec, req, "/app/projects")
	assert.Equal(t, http.StatusSeeOther, rec.Code)
	assert.Equal(t, "/app/projects", rec.Header().Get("Location"))

	// htmx clients get an AJAX navigation scoped to the content box. The target
	// is what keeps the shell — and the clerk-js widgets mounted in it — alive;
	// HX-Location defaults to swapping <body>, which would destroy them.
	req = httptest.NewRequest("POST", "/x", nil)
	req.Header.Set("HX-Request", "true")
	rec = httptest.NewRecorder()
	Navigate(rec, req, "/app/projects")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get("HX-Redirect"))
	assert.JSONEq(t,
		`{"path":"/app/projects","target":"#content","select":"#content","swap":"outerHTML transition:true show:top"}`,
		rec.Header().Get("HX-Location"))

	// The navigation it triggers must round-trip back to a full page, or htmx
	// would find no #content to select. Guards the Navigate/wantsFragment pair.
	nav := httptest.NewRequest("GET", "/app/projects", nil)
	nav.Header.Set("HX-Request", "true")
	nav.Header.Set("HX-Request-Type", "partial") // beta6 mislabels ajax-API selects
	nav.Header.Set("HX-Target", "main#content")
	assert.False(t, wantsFragment(nav))

	// A server-driven navigation must be indistinguishable from a clicked nav
	// link, so both sides read the same constants. Rendering the sidebar proves
	// the template really emits them rather than a drifted literal.
	assert.Equal(t, templates.NavTarget, ContentTarget)
	assert.Equal(t, templates.NavSwap, NavSwap)
	shell := renderToString(t, templates.Sidebar(Page{Path: "/app"}))
	assert.Contains(t, shell, `hx-target="`+ContentTarget+`"`)
	assert.Contains(t, shell, `hx-select="`+ContentTarget+`"`)
	assert.Contains(t, shell, `hx-swap="`+NavSwap+`"`)
	// …and no link drifted back to a hand-written swap.
	assert.NotContains(t, shell, `hx-swap="outerHTML"`)
	assert.Equal(t,
		strings.Count(shell, `hx-boost="true"`),
		strings.Count(shell, `hx-swap="`+NavSwap+`"`),
		"every boosted link must carry the shared nav swap")
}

func TestToast(t *testing.T) {
	rec := httptest.NewRecorder()
	Toast(rec, "success", "Saved")
	assert.JSONEq(t, `{"toast":{"type":"success","message":"Saved","flash":false}}`, rec.Header().Get("HX-Trigger"))

	rec = httptest.NewRecorder()
	FlashToast(rec, "success", "Saved")
	assert.JSONEq(t, `{"toast":{"type":"success","message":"Saved","flash":true}}`, rec.Header().Get("HX-Trigger"))
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
