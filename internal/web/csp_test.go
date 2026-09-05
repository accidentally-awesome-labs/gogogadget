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

// The header a Clerk-selected project serves, byte for byte, in the order and
// spelling v0.7.1 sent it. Every one of the ten directives is here on purpose:
// this is the assertion that a mechanism replacing a hardcoded assembly did
// not quietly reorder, drop or widen anything.
func TestClerkSelectedPolicyIsByteIdenticalToTheHardcodedOne(t *testing.T) {
	policy := policyFor(t, "production", map[string]string{
		"CLERK_FRONTEND_API_URL": "https://clerk.example.com",
	})

	assert.Equal(t, strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https://img.clerk.com",
		"font-src 'self'",
		"connect-src 'self' https://clerk.example.com",
		"worker-src 'self' blob:",
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
