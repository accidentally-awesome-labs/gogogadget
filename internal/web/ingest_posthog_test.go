package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ingestServer stands up the full stack in an environment that does or does
// not select this adapter. Selection is the only gate: nothing here sets a
// credential to turn the route on.
func ingestServer(t *testing.T, environment, upstream string) *Server {
	t.Helper()
	return integrationServer(t, func(d *Deps) {
		d.Config.Env = environment
		d.Config.Values["POSTHOG_HOST"] = upstream
	})
}

// The route is declared, so the policy the middleware applies is the declared
// one. Asserted through the real chain rather than by reading the manifest:
// a POST with no CSRF token must not 403, and 200 requests in a row must not
// 429 under a limiter configured for 100/min.
func TestPostHogIngestProxiesThroughTheFullChain(t *testing.T) {
	var hits int
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := ingestServer(t, "production", upstream.URL)

	code, _, body := serve(t, s, "POST", "/ingest/e/", []byte(`{"event":"test"}`), nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, 1, hits)
	assert.Equal(t, "/e/", gotPath, "the /ingest prefix is stripped before forwarding")
	assert.NotContains(t, body, "Forbidden")

	// The head slot points array.js at /ingest/static/array.js, so the GET
	// method has to be declared too — this is the live coupling between the
	// slot work and this route.
	code, _, _ = serve(t, s, "GET", "/ingest/static/array.js", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, "/static/array.js", gotPath)

	// Rate exemption, through the limiter the server actually has.
	for range 200 {
		if code, _, _ := serve(t, s, "POST", "/ingest/e/", []byte(`{}`), nil); code != http.StatusOK {
			t.Fatalf("rate-exempt route answered %d", code)
		}
	}
}

// The declared policy is what the matcher resolves for a subtree request, so
// the exemptions are readable in one place instead of living in two
// hand-written path prefixes in the middleware.
func TestPostHogIngestPolicyIsTheDeclaredOne(t *testing.T) {
	s := ingestServer(t, "production", "https://example.test")

	request := httptest.NewRequest("POST", "/ingest/e/", strings.NewReader("{}"))
	policy, declared := s.policies.policyFor(request)
	require.True(t, declared, "a declared route must resolve to a policy")
	assert.True(t, policy.CSRFExempt)
	assert.NotEmpty(t, policy.CSRFReason, "csrf_exempt without a reason is refused at generation")
	assert.True(t, policy.RateExempt)
	assert.Equal(t, int64(1<<20), policy.MaxBodyBytes, "a telemetry firehose is capped tighter than the 10 MB global")
	assert.False(t, policy.MaintenanceExempt, "telemetry is not worth serving during maintenance")
}

// In an environment that selects another analytics adapter the route must not
// exist at all — not answer 503, not proxy nowhere. providerActive on the
// generated record is what removes it, the same gate the shell slots use.
func TestPostHogIngestAbsentWhenAdapterNotSelected(t *testing.T) {
	s := ingestServer(t, "test", "https://example.test")
	// GET is CSRF-safe, so a 404 here is the mux speaking: no route.
	code, _, _ := serve(t, s, "GET", "/ingest/static/array.js", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)

	// The POST answers 403, and that is the point: with the route gone its
	// exemption goes with it, so an unregistered path falls closed on CSRF
	// instead of keeping a hand-written prefix exemption alive for something
	// nothing serves.
	code, _, _ = serve(t, s, "POST", "/ingest/e/", []byte(`{}`), nil)
	assert.Equal(t, http.StatusForbidden, code)
	// And with no route there is no policy, so the chain falls closed rather
	// than keeping an exemption alive for a path nothing serves.
	request := httptest.NewRequest("POST", "/ingest/e/", strings.NewReader("{}"))
	_, declared := s.policies.policyFor(request)
	assert.False(t, declared)
	assert.False(t, s.structurallyCSRFExempt(request),
		"the analytics proxy must not be structurally exempt any more")
}

// A misconfigured endpoint answers rather than panics: telemetry must never
// take a page down. The key is required when the adapter is selected, but a
// URL is still something an operator can get wrong.
func TestPostHogIngestUnparseableEndpointAnswers503(t *testing.T) {
	s := ingestServer(t, "production", "not-a-url")

	code, _, _ := serve(t, s, "POST", "/ingest/e/", []byte(`{}`), nil)
	assert.Equal(t, http.StatusServiceUnavailable, code)
}
