package web

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/apphost"
	"github.com/gogogadget/gogogadget/internal/config"
	identityclerk "github.com/gogogadget/gogogadget/internal/identity/clerk"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dev surface exists only in zero-account mode, which config refuses under
// APP_ENV=production. These tests pin the paths, the gate, and the 404 - an
// unknown name has to look like every other missing page, or the surface becomes
// a way to enumerate what exists.

func TestGalleryFamilyPageResolves(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/gallery/actions", nil, nil)

	require.Equal(t, http.StatusOK, code)
	// The family page names itself, so a deep link is self-describing.
	assert.Contains(t, body, "Actions")
	// And it lists components belonging to that family, not every component.
	assert.Contains(t, body, "button")
}

func TestGalleryComponentPageResolves(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/gallery/actions/button", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "button")
}

// A component under the wrong family must not resolve. Accepting it would make
// the family segment decorative, and two URLs for one page is two things to keep
// in sync.
func TestComponentMustMatchItsFamily(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := serve(t, s, "GET", "/dev/gallery/data/button", nil, nil)

	assert.Equal(t, http.StatusNotFound, code)
}

func TestUnknownGalleryNamesAreNotFound(t *testing.T) {
	s := integrationServer(t, nil)

	for _, path := range []string{
		"/dev/gallery/nonsense",
		"/dev/gallery/actions/nonsense",
		"/dev/scenarios/nonsense",
	} {
		code, _, _ := serve(t, s, "GET", path, nil, nil)
		assert.Equal(t, http.StatusNotFound, code, "%s must 404", path)
	}
}

func TestScenarioPageResolves(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/scenarios/system-states", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "system-states")
}

// The dev surface must never appear in production navigation. A link is how a
// dev-only page ends up in a sitemap, a crawler, and a support ticket.
func TestDevRoutesAreNotInProductionNavigation(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/", nil, nil)
	require.Equal(t, http.StatusOK, code)

	assert.NotContains(t, body, `href="/dev/gallery`)
	assert.NotContains(t, body, `href="/dev/scenarios`)
}

// Interactive examples post to real handlers with real CSRF. A no-op button
// proves nothing about a component whose whole behaviour is the request it makes.
func TestDevFragmentRouteRequiresCSRF(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := serve(t, s, "POST", "/dev/ui/kanban/move",
		[]byte("card=card-catalog&from=backlog&to=done"),
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})

	// Rejected before the handler: the route is not exempt.
	assert.Equal(t, http.StatusForbidden, code)
}

// An unknown component or action on the fragment route is a 404, not a silent
// 200 - a demo wired to a typo would otherwise look like it worked.
func TestUnknownFragmentActionIsNotFound(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := serve(t, s, "GET", "/dev/ui/nonsense/thing", nil, nil)

	assert.Equal(t, http.StatusNotFound, code)
}

// Every dev route must be gated. A route registered without the gate is one
// APP_ENV away from being public.
func TestEveryDevRouteIsGated(t *testing.T) {
	for _, route := range RouteRegistry {
		if !strings.HasPrefix(route.Pattern, "/dev/") {
			continue
		}
		assert.Equal(t, ScopeDev, route.Scope, "%s must be dev-scoped", route.ID)
		require.NotNil(t, route.Enabled, "%s must declare an enable gate", route.ID)
	}
}

// The generated registry must carry the whole documented surface, so a missing
// route is a build-time fact rather than a 404 someone reports later.
func TestDevSurfaceIsCompletelyRegistered(t *testing.T) {
	want := map[string]string{
		"GET /dev/gallery":                      "",
		"GET /dev/gallery/{family}":             "",
		"GET /dev/gallery/{family}/{component}": "",
		"GET /dev/scenarios/{scenario}":         "",
		"GET /dev/ui/{component}/{action}":      "",
		"POST /dev/ui/{component}/{action}":     "",
		"DELETE /dev/ui/{component}/{action}":   "",
	}
	for _, route := range RouteRegistry {
		delete(want, route.Method+" "+route.Pattern)
	}
	assert.Empty(t, want, "documented dev routes missing from the registry")
}

// productionServer builds a Server from a configuration that actually passed
// production validation, so the dev gate the tests below read is the value a
// deployed binary computes rather than a literal the test chose. The identity
// ports come from the real module constructor for the same reason.
//
// The helper checks its own output before returning. A helper that quietly
// handed back a dev-configured server would make every assertion below pass
// while proving nothing, which is the only way these tests can lie.
func productionServer(t *testing.T) *Server {
	t.Helper()
	env := map[string]string{
		"APP_ENV":                      "production",
		"APP_URL":                      "https://app.example.com",
		"DATABASE_URL":                 "postgres://unused.example/production",
		"CLERK_SECRET_KEY":             "sk_live_fixture",
		"CLERK_WEBHOOK_SECRET":         testWebhookSecret,
		"CLERK_PORTAL_URL":             "https://accounts.example.com",
		"CLERK_PUBLISHABLE_KEY":        "pk_live_fixture",
		"RESEND_API_KEY":               "re_fixture",
		"NEON_API_KEY":                 "neon_fixture",
		"STORAGE_R2_ACCESS_KEY_ID":     "ak_fixture",
		"STORAGE_R2_ACCOUNT_ID":        "acct_fixture",
		"STORAGE_R2_BUCKET":            "bucket_fixture",
		"STORAGE_R2_SECRET_ACCESS_KEY": "secret_fixture",
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	require.NoError(t, err, "fixture must be a configuration production accepts")
	require.True(t, cfg.Production(), "fixture must resolve to APP_ENV=production")
	require.False(t, cfg.DevAuthBypass, "fixture must not carry the dev bypass")

	ident, err := identityclerk.NewModule(
		context.Background(),
		apphost.Map(env, cfg.Now(), "test"),
		identityclerk.Deps{Config: &cfg},
	)
	require.NoError(t, err)
	require.IsType(t, &identityclerk.Verifier{}, ident.Verifier,
		"a production identity closure verifies against Clerk, not the fake")

	s := integrationServer(t, func(d *Deps) {
		d.Config = &cfg
		d.Verifier = ident.Verifier
		d.Fetcher = ident.Fetcher
		d.IdentityDeleter = ident.Deleter
	})
	require.True(t, s.cfg.Production(), "the built server must carry the production config")
	require.False(t, s.devAuthBypass(), "the built server must have the dev gate closed")
	return s
}

// devLivePaths are the concrete dev URLs a zero-account clone serves. Listed
// rather than derived from the patterns: a wildcard filled with a made-up
// segment 404s in both configurations, so it would prove nothing.
func devLivePaths() []string {
	return []string{
		"/dev/gallery",
		"/dev/gallery/actions",
		"/dev/gallery/actions/button",
		"/dev/scenarios/system-states",
		"/dev/login",
		"/dev/switch-org",
	}
}

// Guarantee one of two: the dev surface is absent from a production build's
// route table. Asserted in both directions - the gate opens on a zero-account
// server and closes on a production one - because a gate that is always shut
// would pass a one-sided check while the surface it guards was already broken.
func TestDevRoutesAreAbsentFromAProductionServer(t *testing.T) {
	prod := productionServer(t)
	dev := integrationServer(t, nil)
	require.False(t, prod.devAuthBypass(), "production config must leave the dev gate closed")
	require.True(t, dev.devAuthBypass())

	// The route table, which covers the whole surface including the wildcard
	// fragment routes no single URL can exercise.
	gated := 0
	for _, route := range RouteRegistry {
		if route.Scope != ScopeDev && !strings.HasPrefix(route.Pattern, "/dev/") {
			continue
		}
		gated++
		require.NotNil(t, route.Enabled, "%s must declare an enable gate", route.ID)
		assert.False(t, route.Enabled(prod), "%s must not register in production", route.ID)
		assert.True(t, route.Enabled(dev), "%s must register in zero-account mode", route.ID)
	}
	require.NotZero(t, gated, "the registry must contain dev routes for this to mean anything")

	// The responses. Every dev URL must answer with the ordinary not-found page,
	// carrying none of the catalog's own markers. Byte equality with a control
	// 404 is not asserted: the page embeds a fresh CSRF token per request.
	for _, path := range devLivePaths() {
		live, _, _ := serve(t, dev, "GET", path, nil, nil)
		require.NotEqual(t, http.StatusNotFound, live,
			"%s must be live in zero-account mode, or its 404 in production proves nothing", path)

		code, _, body := serve(t, prod, "GET", path, nil, nil)
		assert.Equal(t, http.StatusNotFound, code, "%s must 404 in production", path)
		assert.Contains(t, body, "<title>Page not found",
			"%s must render the ordinary not-found page", path)
		for _, marker := range []string{"gallery-nav", "family-index", "scenario-surface"} {
			assert.NotContains(t, body, marker, "%s must not leak %s", path, marker)
		}
	}
}

// Guarantee two of two: even if someone sets the gate deliberately, a
// production configuration refuses to load, so no production process can reach
// a Server whose dev gate is open. internal/config owns the rule
// (TestLoadDevAuthBypassRefusedInProduction); this pins the dev surface's
// dependency on it, because the gate above is only as good as the refusal.
func TestDevGateCannotLoadUnderProduction(t *testing.T) {
	env := map[string]string{
		"APP_ENV":                      "production",
		"APP_URL":                      "https://app.example.com",
		"DATABASE_URL":                 "postgres://unused.example/production",
		"CLERK_SECRET_KEY":             "sk_live_fixture",
		"CLERK_WEBHOOK_SECRET":         testWebhookSecret,
		"CLERK_PORTAL_URL":             "https://accounts.example.com",
		"CLERK_PUBLISHABLE_KEY":        "pk_live_fixture",
		"RESEND_API_KEY":               "re_fixture",
		"NEON_API_KEY":                 "neon_fixture",
		"STORAGE_R2_ACCESS_KEY_ID":     "ak_fixture",
		"STORAGE_R2_ACCOUNT_ID":        "acct_fixture",
		"STORAGE_R2_BUCKET":            "bucket_fixture",
		"STORAGE_R2_SECRET_ACCESS_KEY": "secret_fixture",
		"DEV_AUTH_BYPASS":              "true",
	}
	_, err := config.LoadFrom(func(k string) string { return env[k] })

	require.Error(t, err)
	assert.EqualError(t, err, "DEV_AUTH_BYPASS=true is refused when APP_ENV=production")
}

// Every internal link the dev catalog renders must resolve. A dead link in the
// reference for a navigation component is the worst possible example, and
// /dev/gallery/other was exactly that for as long as it existed.
func TestDevCatalogHasNoDeadInternalLinks(t *testing.T) {
	s := integrationServer(t, nil)
	// The demo user the dev session names has to exist, or every app-layout
	// scenario answers with the bounded retry instead of its page.
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")

	// An app-layout scenario needs the session it would otherwise redirect to
	// obtain, so the crawl carries one throughout.
	session := sessionCookie("user_demo", "org_demo", "org:admin")
	pages := []string{"/dev/gallery", "/dev/gallery/actions", "/dev/scenarios/dashboard"}
	seen := map[string]bool{}
	for _, page := range pages {
		code, _, body := serve(t, s, "GET", page, nil, nil, session)
		require.Equal(t, http.StatusOK, code, page)

		for _, href := range hrefsIn(body) {
			if !strings.HasPrefix(href, "/dev/") || seen[href] {
				continue
			}
			seen[href] = true
			linked, linkHeaders, _ := serve(t, s, "GET", href, nil, nil, session)
			// An app-layout scenario answers 303 first to issue the dev session.
			// Following it is the stronger check: it proves the destination
			// renders, not just that something answered.
			if linked == http.StatusSeeOther {
				location := linkHeaders.Get("Location")
				require.NotEmpty(t, location, "%s redirected without a destination", href)
				linked, _, _ = serve(t, s, "GET", location, nil, nil, session)
			}
			assert.Equal(t, http.StatusOK, linked, "%s links to %s", page, href)
		}
	}
	require.NotEmpty(t, seen, "the catalog should link somewhere")
}

// hrefsIn pulls the href values out of rendered HTML. Deliberately crude: it
// only has to find links, and a real parser here would be a dependency for a
// substring search.
func hrefsIn(body string) []string {
	var out []string
	rest := body
	for {
		i := strings.Index(rest, `href="`)
		if i < 0 {
			return out
		}
		rest = rest[i+6:]
		j := strings.Index(rest, `"`)
		if j < 0 {
			return out
		}
		out = append(out, rest[:j])
		rest = rest[j:]
	}
}

// The fragment route returns a fragment, not a page. Returning the gallery would
// nest the whole page inside the region being swapped, which renders and is
// wrong - the swap looks like it worked.
func TestPagerFragmentIsNotAPage(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/ui/pagination/page?page=3", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Page 3 of 5.")
	// No shell, and no repeat of the id it swaps into.
	assert.NotContains(t, body, "<html")
	assert.NotContains(t, body, `id="gallery-pager"`)
}

// The fragment reads its parameters the way a real paged table does, so the demo
// exercises the request shape rather than a hardcoded page.
func TestPagerFragmentCarriesItsParameters(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, body := serve(t, s, "GET", "/dev/ui/search/query?q=banner&page=2", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "filtered by banner")
	assert.Contains(t, body, "Page 2 of 5")
}

// An out-of-range page must clamp rather than render a pager whose current
// position does not exist.
func TestPagerFragmentClampsOutOfRange(t *testing.T) {
	s := integrationServer(t, nil)

	_, _, high := serve(t, s, "GET", "/dev/ui/pagination/page?page=99", nil, nil)
	_, _, low := serve(t, s, "GET", "/dev/ui/pagination/page?page=-3", nil, nil)

	assert.Contains(t, high, "Page 5 of 5")
	assert.Contains(t, low, "Page 1 of 5")
}

// A mutating method on a dev fragment route is CSRF-checked like every other
// mutation. The route being dev-only does not exempt it.
func TestFragmentRouteEnforcesCSRFOnMutations(t *testing.T) {
	s := integrationServer(t, nil)

	code, _, _ := serve(t, s, "DELETE", "/dev/ui/pagination/page", nil, nil)

	assert.Equal(t, http.StatusForbidden, code)
}

// The fragment routes accept only the methods their examples use: a GET-only
// example answering POST is a surface nobody reviewed. Checked past CSRF, so
// the 404 comes from the handler rather than the middleware.
func TestFragmentRouteRejectsWrongMethod(t *testing.T) {
	s := integrationServer(t, nil)
	token, cookies := csrfFor(t, s)
	headers := http.Header{}
	headers.Set("X-CSRF-Token", token)

	code, _, _ := serve(t, s, "POST", "/dev/ui/pagination/page", nil, headers, cookies...)

	assert.Equal(t, http.StatusNotFound, code)
}

// An app-layout scenario renders the signed-in shell, which bounces a visitor
// with no session. It issues the same synthetic session /dev/login does and
// re-enters through the real middleware, rather than fabricating context and
// rendering a shell that behaves unlike production.
func TestAppScenarioIssuesADevSession(t *testing.T) {
	s := integrationServer(t, nil)

	code, headers, _ := serve(t, s, "GET", "/dev/scenarios/billing", nil, nil)

	require.Equal(t, http.StatusSeeOther, code)
	// Values, not Get: the CSRF cookie is always set, so reading only the first
	// header would test which cookie happens to be written first.
	assert.Contains(t, strings.Join(headers.Values("Set-Cookie"), " "), "e2e:user_demo:org_demo")
	assert.Equal(t, "/dev/scenarios/billing?session=retried", headers.Get("Location"))
}

// A public scenario must not be handed a session it never asked for.
func TestPublicScenarioNeedsNoSession(t *testing.T) {
	s := integrationServer(t, nil)

	code, headers, body := serve(t, s, "GET", "/dev/scenarios/system-states", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, strings.Join(headers.Values("Set-Cookie"), " "), "e2e:")
	assert.Contains(t, body, "System states")
}

// The redirect must preserve the state the visitor asked for, or every app
// scenario lands on default after signing in.
func TestScenarioSessionRedirectKeepsTheState(t *testing.T) {
	s := integrationServer(t, nil)

	_, headers, _ := serve(t, s, "GET", "/dev/scenarios/billing?state=error", nil, nil)

	location := headers.Get("Location")
	assert.Contains(t, location, "state=error")
	assert.Contains(t, location, "session=retried")
}

// The retry is one-shot. An unbounded "set the cookie and try again" loops
// forever on a database that has never been seeded, which is a worse failure
// than rendering the scenario in a shell that needs no session.
func TestScenarioSessionRetryDoesNotLoop(t *testing.T) {
	s := integrationServer(t, nil)

	code, headers, body := serve(t, s, "GET", "/dev/scenarios/billing?session=retried", nil, nil)

	require.Equal(t, http.StatusOK, code)
	assert.Empty(t, headers.Get("Location"))
	assert.Contains(t, body, "Billing")
}

// Every declared scenario must have a composition. A descriptor with no Render
// renders a stated placeholder, which is honest but is not a scenario - so the
// registry claiming twelve surfaces has to mean twelve.
func TestEveryScenarioHasAComposition(t *testing.T) {
	var missing []string
	for _, scenario := range templates.ScenarioRegistry {
		if scenario.Render == nil {
			missing = append(missing, scenario.Slug)
		}
	}
	assert.Empty(t, missing, "scenarios declared without a composition")
}

// Each declared state must render a materially different surface. A state
// control that changes nothing is worse than no control: it teaches the reader
// that the state does not matter.
func TestEachScenarioStateRendersDifferently(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	for _, scenario := range templates.ScenarioRegistry {
		seen := map[string]string{}
		for _, state := range append([]string{"default"}, scenario.States...) {
			url := "/dev/scenarios/" + scenario.Slug
			if state != "default" {
				url += "?state=" + state
			}
			code, _, body := serve(t, s, "GET", url, nil, nil, session)
			require.Equal(t, http.StatusOK, code, "%s %s", scenario.Slug, state)
			// The surface region only, so shell differences never count as a
			// state difference.
			surface := body
			if i := strings.Index(body, `data-testid="scenario-surface"`); i >= 0 {
				surface = body[i:]
			}
			for other, prior := range seen {
				if other == state {
					continue
				}
				assert.NotEqual(t, prior, surface,
					"%s renders %q and %q identically", scenario.Slug, other, state)
			}
			seen[state] = surface
		}
	}
}

// A scenario renders inside #content. Emitting shell markup would let it drift
// from the chrome the product actually ships.
func TestScenariosRenderNoShellMarkup(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	for _, scenario := range templates.ScenarioRegistry {
		_, _, body := serve(t, s, "GET", "/dev/scenarios/"+scenario.Slug, nil, nil, session)
		i := strings.Index(body, `data-testid="scenario-surface"`)
		require.GreaterOrEqual(t, i, 0, "%s has no surface region", scenario.Slug)
		surface := body[i:]
		// "<head" is deliberately not checked: it also matches <header>, which
		// KanbanColumn legitimately renders inside content. The shell elements
		// that must never appear are the document ones.
		for _, forbidden := range []string{"<html", "<body", "<nav id=", "<aside"} {
			assert.NotContains(t, surface, forbidden, "%s emits shell markup", scenario.Slug)
		}
	}
}

// Two renders of the same URL must be byte-identical, or the visual baselines
// this feeds can never be trusted.
func TestScenariosAreDeterministic(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	// The surface region only, with its tokens normalised. Two things are
	// nondeterministic by design and neither is fixture data: the shell carries a
	// fresh CSRF token on every response, and every form inside the surface
	// carries the same token masked with a fresh one-time pad. Comparing the
	// bytes around them is the point of this test; comparing the pad is not.
	surfaceOf := func(body string) string {
		i := strings.Index(body, `data-testid="scenario-surface"`)
		if i < 0 {
			return normalizeCSRF(body)
		}
		return normalizeCSRF(body[i:])
	}
	for _, scenario := range templates.ScenarioRegistry {
		url := "/dev/scenarios/" + scenario.Slug
		_, _, first := serve(t, s, "GET", url, nil, nil, session)
		_, _, second := serve(t, s, "GET", url, nil, nil, session)
		assert.Equal(t, surfaceOf(first), surfaceOf(second),
			"%s is not deterministic", scenario.Slug)
	}
}

// Every context axis is validated against a closed set. An unrecognised value is
// refused rather than falling back: a URL that looks like it selected rtl and
// rendered ltr is a reviewer arguing about a screenshot that was never taken.
func TestScenarioContextAxesAreValidated(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	valid := []string{
		"?text=ltr", "?text=rtl", "?content=normal", "?content=long",
		"?density=comfortable", "?density=compact",
		"?text=rtl&content=long&state=empty",
	}
	for _, query := range valid {
		code, _, _ := serve(t, s, "GET", "/dev/scenarios/resource-list"+query, nil, nil, session)
		assert.Equal(t, http.StatusOK, code, "%s must be accepted", query)
	}

	invalid := []string{"?text=sideways", "?content=medium", "?density=cosy", "?state=nonsense"}
	for _, query := range invalid {
		code, _, _ := serve(t, s, "GET", "/dev/scenarios/resource-list"+query, nil, nil, session)
		assert.Equal(t, http.StatusNotFound, code, "%s must be refused", query)
	}
}

// The direction has to reach the markup, or the control is decoration.
func TestDirectionReachesTheSurface(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	_, _, ltr := serve(t, s, "GET", "/dev/scenarios/settings", nil, nil, session)
	_, _, rtl := serve(t, s, "GET", "/dev/scenarios/settings?text=rtl", nil, nil, session)

	assert.Contains(t, rtl, `dir="rtl"`)
	assert.NotContains(t, ltr, `dir="rtl"`)
}

// Long content must actually change what is rendered. A toggle that does nothing
// hides exactly the layout break it exists to expose.
func TestLongContentChangesTheSurface(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	surfaceOf := func(body string) string {
		i := strings.Index(body, `data-testid="scenario-surface"`)
		require.GreaterOrEqual(t, i, 0)
		return body[i:]
	}
	_, _, normal := serve(t, s, "GET", "/dev/scenarios/dashboard", nil, nil, session)
	_, _, long := serve(t, s, "GET", "/dev/scenarios/dashboard?content=long", nil, nil, session)

	assert.NotEqual(t, surfaceOf(normal), surfaceOf(long))
}

// Every axis control is a real link carrying the other axes, so a reviewer can
// combine them and share the result.
func TestAxisControlsPreserveTheOtherAxes(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	_, _, body := serve(t, s, "GET",
		"/dev/scenarios/resource-list?state=empty&text=rtl", nil, nil, session)

	// The content toggle must keep the state and direction already chosen.
	assert.Contains(t, body, "content=long")
	assert.Contains(t, body, "state=empty")
	assert.Contains(t, body, "text=rtl")
}

// A control that changes nothing is the same lie as a disabled button with no
// explanation: the reader clicks, sees no difference, and concludes the axis
// does not matter. So every axis a scenario offers must move its surface.
func TestEveryOfferedAxisMovesTheSurface(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	surfaceOf := func(body string) string {
		i := strings.Index(body, `data-testid="scenario-surface"`)
		require.GreaterOrEqual(t, i, 0)
		return body[i:]
	}
	for _, scenario := range templates.ScenarioRegistry {
		base := "/dev/scenarios/" + scenario.Slug
		_, _, plain := serve(t, s, "GET", base, nil, nil, session)
		offered := map[string]bool{}
		for _, line := range strings.Split(plain, "\n") {
			for _, key := range []string{"density", "content", "text"} {
				if strings.Contains(line, `data-testid="axis-`+key+`"`) {
					offered[key] = true
				}
			}
		}
		for key, probe := range map[string]string{
			"density": "?density=compact",
			"content": "?content=long",
			"text":    "?text=rtl",
		} {
			if !offered[key] {
				continue
			}
			_, _, moved := serve(t, s, "GET", base+probe, nil, nil, session)
			assert.NotEqual(t, surfaceOf(plain), surfaceOf(moved),
				"%s offers %s but renders identically", scenario.Slug, key)
		}
	}
}

// An axis a scenario cannot respond to must not be offered at all, rather than
// offered and inert.
func TestUnusableAxisIsNotOffered(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_demo", "org_demo", "org:admin")
	session := sessionCookie("user_demo", "org_demo", "org:admin")

	// Communication holds no table or list that takes a Density.
	_, _, body := serve(t, s, "GET", "/dev/scenarios/communication", nil, nil, session)
	assert.NotContains(t, body, `data-testid="axis-density"`)

	// Resource list is built on a DataTable, so it does.
	_, _, table := serve(t, s, "GET", "/dev/scenarios/resource-list", nil, nil, session)
	assert.Contains(t, table, `data-testid="axis-density"`)
}

// Every fragment action must have a caller. A handler nothing invokes is code
// with no reader: the endpoint answers, the demo it exists for never appears,
// and the gap is invisible because both halves look complete on their own.
func TestEveryFragmentActionIsReachableFromTheGallery(t *testing.T) {
	s := integrationServer(t, nil)

	_, _, body := serve(t, s, "GET", "/dev/gallery", nil, nil)

	// The gallery is where the interactive examples live, so each declared
	// action has to appear in its markup as a request the page can make.
	for _, action := range []string{
		"toast/show", "copy/confirm", "upload/receive", "row/delete",
		"table/sort", "form/save", "calendar/select", "editor/preview",
		"overlay/open",
	} {
		assert.Contains(t, body, "/dev/ui/"+action,
			"no example invokes /dev/ui/%s", action)
	}
}

// The interactive examples must exercise production's status codes, not a
// friendlier set. A demo that answers 200 where the product answers 422 teaches
// the reader a contract that does not exist.
func TestInteractiveExamplesUseProductionStatuses(t *testing.T) {
	s := integrationServer(t, nil)
	token, cookies := csrfFor(t, s)
	headers := http.Header{}
	headers.Set("X-CSRF-Token", token)
	headers.Set("Content-Type", "application/x-www-form-urlencoded")

	// A rejected value is a re-rendered fragment, never a redirect or a 200.
	code, _, body := serve(t, s, "POST", "/dev/ui/form/save",
		[]byte("dev_email=not-an-email"), headers, cookies...)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	assert.NotContains(t, body, "<html")

	// A row delete is 200 with nothing in it, so outerHTML removes the row.
	code, _, body = serve(t, s, "DELETE", "/dev/ui/row/delete?row=apollo", nil, headers, cookies...)
	assert.Equal(t, http.StatusOK, code)
	assert.Empty(t, strings.TrimSpace(body), "a delete that returns markup leaves the row behind")
}
