package web

import (
	"net/http"
	"strings"
	"testing"

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
