package web

import (
	"net/http/httptest"

	"github.com/gogogadget/gogogadget/internal/config"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyFor drives the real middleware chain and returns the header a browser
// would receive. Asserting the header rather than the assembly is the point:
// the assembly moved, the bytes must not have.
func policyFor(t *testing.T, environment string, values map[string]string) string {
	t.Helper()
	s := testServer(t, func(cfg *config.Config) {
		cfg.Env = environment
		// The fixture pre-seeds CLERK_FRONTEND_API_URL; a nil values map here
		// means "whatever the fixture has", and an explicit one replaces it so
		// a test can assert the unconfigured case.
		if values != nil {
			cfg.Values = map[string]string{}
			for key, value := range values {
				cfg.Values[key] = value
			}
		}
	})
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/healthz", nil))
	policy := recorder.Header().Get("Content-Security-Policy")
	require.NotEmpty(t, policy, "a page with no policy is a page with no protection")
	return policy
}

// The header a Clerk-selected project serves, compared against the exact
// string v0.7.1's hardcoded assembly produced — directives in that order, with
// that spelling. The literal below is transcribed from the v0.7.1 source
// (middleware.go's strings.Join list), so it is a golden rather than a
// tautology: nothing in this change generated it.
//
// media-src and frame-src joined the table and are absent here because they
// inherit default-src until something contributes to them.
func TestClerkSelectedPolicyMatchesTheHardcodedHeader(t *testing.T) {
	policy := policyFor(t, "production", map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	assert.Equal(t, strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"worker-src 'self' blob:",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https://img.clerk.com",
		"font-src 'self'",
		"connect-src 'self' https://clerk.example.com",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; "), policy)
}

// A project whose identity slot selects another adapter gets no vendor origin,
// no avatar host, and — the one that matters most — no blob: worker source.
// That relaxation was carried unconditionally by every project because one
// vendor's session handshake needs it.
func TestPolicyWithoutClerkCarriesNoVendorSourceAndNoBlob(t *testing.T) {
	policy := policyFor(t, "test", map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	for _, want := range []string{
		"connect-src 'self'", "img-src 'self' data:", "worker-src 'self'",
	} {
		assert.Contains(t, policy, want+";", "directive %q must stand alone", want)
	}
	assert.NotContains(t, policy, "img.clerk.com")
	assert.NotContains(t, policy, "blob:")
	assert.NotContains(t, strings.ToLower(policy), "clerk")

	// The posture is unchanged in both directions.
	assert.Contains(t, policy, "script-src 'self';")
	assert.NotContains(t, policy, "unsafe-eval")
}

// A contribution may only add to a directive its manifest granted, and only a
// source the grammar allows. Both are plan-time refusals; this is the runtime
// half, which has to hold because a source can arrive from configuration and
// therefore could not be checked when the plan was made.
func TestPolicyDropsUngrantedDirectivesAndInvalidSources(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"rogue": func(map[string]string) map[string][]string {
			return map[string][]string{
				// Granted, and every source refused for a different reason.
				"img-src": {
					"'unsafe-inline'", "*", "http://plain.example.com",
					"https://*.*.example.com", "https://ok.example.com",
				},
				// Not granted at all.
				"script-src": {"https://cdn.example.com"},
			}
		},
	}
	CSPDirectiveGrants = map[string][]string{"rogue": {"img-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "img-src 'self' data: https://ok.example.com;",
		"the one valid source is added and the rest are dropped")
	assert.Contains(t, policy, "script-src 'self';", "an ungranted directive is never widened")

	// Scoped to the directive that WAS granted: 'unsafe-inline' legitimately
	// appears in the base style-src, so asserting over the whole header would
	// pass for the wrong reason or fail for one.
	imgSrc := ""
	for _, part := range strings.Split(policy, "; ") {
		if strings.HasPrefix(part, "img-src ") {
			imgSrc = part
		}
	}
	require.NotEmpty(t, imgSrc)
	for _, refused := range []string{"unsafe-inline", "http://", "*.*.", " * "} {
		assert.NotContains(t, imgSrc, refused, "img-src must carry only the valid source")
	}
	assert.NotContains(t, policy, "cdn.example.com", "an ungranted directive's sources never appear")
}

// A directive with no base sources and no contribution is not rendered at all,
// so default-src governs it. An empty directive is invalid CSP and a rendered
// one would be looser than absence.
func TestPolicyOmitsDirectivesNothingContributesTo(t *testing.T) {
	policy := policyFor(t, "test", map[string]string{})

	assert.NotContains(t, policy, "media-src")
	assert.NotContains(t, policy, "frame-src")
	assert.NotContains(t, policy, ";;")
	assert.False(t, strings.HasSuffix(policy, ";"))
	for _, part := range strings.Split(policy, "; ") {
		assert.NotEmpty(t, strings.TrimSpace(part))
		assert.Equal(t, len(strings.Fields(part)), len(strings.Split(part, " ")),
			"no directive may carry a double space: %q", part)
	}
}

// The header is composed once, so two requests cannot disagree and a
// contribution cannot observe a request.
func TestPolicyIsStableAcrossRequests(t *testing.T) {
	s := testServer(t, func(cfg *config.Config) { cfg.Env = "production" })
	seen := map[string]struct{}{}
	for range 5 {
		recorder := httptest.NewRecorder()
		s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/healthz", nil))
		seen[recorder.Header().Get("Content-Security-Policy")] = struct{}{}
	}
	assert.Len(t, seen, 1)
}

// A directive that inherits default-src must restate 'self' the moment it is
// rendered. CSP's override rule is the whole reason: `frame-src https://vendor`
// REPLACES default-src for frames, so without this every same-origin iframe in
// the app breaks the first time a contribution uses one of these grants.
func TestContributedInheritingDirectivesKeepSelf(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"widget": func(map[string]string) map[string][]string {
			return map[string][]string{
				"frame-src": {"https://widget.example.com"},
				"media-src": {"https://media.example.com"},
			}
		},
	}
	CSPDirectiveGrants = map[string][]string{"widget": {"frame-src", "media-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "media-src 'self' https://media.example.com;")
	assert.Contains(t, policy, "frame-src 'self' https://widget.example.com;")
}

// Hostnames are case-insensitive, so an operator-supplied origin that differs
// only in case must reach the header rather than being dropped — and it must
// arrive in one canonical spelling, so two cases of one origin cannot both
// survive dedupe and read as two permissions.
func TestContributedOriginIsCanonicalisedNotDropped(t *testing.T) {
	originalProviders, originalGrants := CSPSourceProviders, CSPDirectiveGrants
	originalKeys, originalActive := CSPValueKeys, CSPActive
	CSPSourceProviders = map[string]CSPSourceProvider{
		"mixed": func(map[string]string) map[string][]string {
			return map[string][]string{"img-src": {
				"https://IMG.Clerk.com", "https://img.clerk.com",
				// Still refused: a path was never part of an origin, and
				// nothing is trimmed into looking narrower than it is.
				"https://img.clerk.com/avatars/",
			}}
		},
	}
	CSPDirectiveGrants = map[string][]string{"mixed": {"img-src"}}
	CSPValueKeys = map[string][]string{}
	CSPActive = map[string]func(string) bool{}
	t.Cleanup(func() {
		CSPSourceProviders, CSPDirectiveGrants = originalProviders, originalGrants
		CSPValueKeys, CSPActive = originalKeys, originalActive
	})

	policy := policyFor(t, "test", map[string]string{})

	assert.Contains(t, policy, "img-src 'self' data: https://img.clerk.com;")
	assert.NotContains(t, policy, "IMG.Clerk.com")
	assert.NotContains(t, policy, "avatars")
}

// The grammar exists twice: modkit.ValidateCSPSource refuses at plan time and
// cspContributableSource enforces at runtime. The runtime copy is the one that
// actually gates what reaches a browser, and nothing fails if it drifts
// LOOSER — so this asserts the two agree on every case the plan-time tests
// cover, in the direction that matters.
func TestRuntimeGrammarAgreesWithThePlanTimeGrammar(t *testing.T) {
	accepted := []string{
		"https://clerk.example.com", "https://img.clerk.com",
		"https://*.clerk.accounts.dev", "https://vendor.example.com:8443",
		"blob:", "data:",
	}
	refused := []string{
		"'unsafe-inline'", "'unsafe-eval'", "'self'", "*", "*.example.com",
		"https://*.*.example.com", "http://plain.example.com",
		"https://ok.example.com/api", "javascript:", "",
		"https://a.example.com https://b.example.com",
		"https://ok.example.com\r\nx-injected: 1",
		"https://*", "https://*.com",
		"https://vendor.example.com:99999",
	}
	for _, source := range accepted {
		assert.Truef(t, cspContributableSource.MatchString(source),
			"the runtime grammar must accept %q, which plan time accepts", source)
	}
	for _, source := range refused {
		assert.Falsef(t, cspContributableSource.MatchString(source),
			"the runtime grammar must refuse %q, which plan time refuses", source)
	}
}
