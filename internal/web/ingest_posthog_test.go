package web

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	// No route-specific cap, which is a decision with evidence behind it.
	// PostHog's proxy reference asks for up to 64 MB per message because large
	// session recordings fail below that. This chain caps every route at
	// globalMaxBodyBytes (10 MiB) in the outermost handler, and a declared
	// policy may only NARROW that — generation refuses a value that does not
	// (`does not narrow the global 10485760-byte cap`), so 64 MB is not
	// expressible on a route at all. Declaring anything here would therefore
	// mean tightening below the 10 MiB this proxy already had, and dropping
	// session recordings that used to get through. Zero means "the chain
	// ceiling governs", which is exactly the pre-existing behaviour.
	// Supporting the vendor maximum needs globalMaxBodyBytes raised for every
	// route: a seam-wide decision, named in the report rather than smuggled in
	// under a telemetry route.
	assert.Zero(t, policy.MaxBodyBytes,
		"a route may only narrow the global cap, and narrowing this one would drop session recordings")
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

// The cap is defended, not merely declared: a body above it must be rejected
// rather than silently truncated into the upstream. This is the failure mode a
// tighter-than-status-quo cap would have introduced for session recordings,
// which is why the declared value is the prior ceiling.
func TestPostHogIngestRejectsABodyOverTheCap(t *testing.T) {
	// The upstream handler runs on the server's goroutine while the test reads
	// the count on its own, and for an over-cap request the proxy gives up
	// before that handler returns — so the count must be synchronized or the
	// read is a data race that only -race reports.
	var mu sync.Mutex
	upstreamBytes := 0
	received := func() int {
		mu.Lock()
		defer mu.Unlock()
		return upstreamBytes
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		mu.Lock()
		upstreamBytes = int(n)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	s := ingestServer(t, "production", upstream.URL)

	oversized := bytes.Repeat([]byte("x"), int(globalMaxBodyBytes)+1024)
	code, _, _ := serve(t, s, "POST", "/ingest/e/", oversized, nil)
	assert.NotEqual(t, http.StatusOK, code, "a body over the cap must not proxy successfully")
	assert.Less(t, received(), len(oversized),
		"the upstream must never receive more than the cap allows")

	// And a body under the cap still goes through untouched, so the cap is a
	// ceiling rather than a throttle. The proxy returns only after the upstream
	// handler has responded, so this read observes that handler's write.
	sized := bytes.Repeat([]byte("y"), 512<<10)
	code, _, _ = serve(t, s, "POST", "/ingest/e/", sized, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, len(sized), received())
}
