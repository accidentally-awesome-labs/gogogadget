package web

import (
	"net/http"
	"sync"

	"github.com/gogogadget/gogogadget/internal/analytics"
)

// handlePostHogIngest serves the same-origin telemetry proxy declared by
// ggg/system/analytics-posthog.
//
// It used to be hand-registered in routes.go behind cfg.PostHogEnabled(), the
// last credential-presence selector in the tree and the one exception that made
// "routes are declared, not registered" a half-truth. Its CSRF and rate-limit
// exemptions lived in prose comments and in two hand-written path prefixes in
// the middleware, so the policy the chain applied and the policy anybody could
// read were two different things.
//
// Now the manifest declares two routes with a real RoutePolicy and this file
// belongs to the adapter. Selection is the gate: the route exists only in the
// environments that select this adapter, which is what providerActive on the
// generated record does, and in those environments POSTHOG_API_KEY is required
// so its absence is a boot refusal rather than a silently missing route.
//
// The browser reaches the vendor through this proxy and never contacts a third
// party directly, which is what keeps the CSP at script-src 'self' — see the
// head slot in internal/web/templates/slots/posthog.templ, which points
// array.js at /ingest/static/array.js.
func (s *Server) handlePostHogIngest(w http.ResponseWriter, r *http.Request) {
	host := s.cfg.Value("POSTHOG_HOST")
	proxy, err := ingestProxy(host)
	if err != nil {
		// The key is required when this adapter is selected, but the endpoint
		// is a URL an operator can still get wrong. Telemetry must never take
		// the page down with it, so this is the one failure mode that answers
		// rather than panics.
		s.log.ErrorContext(r.Context(), "analytics ingest proxy", "error", err)
		http.Error(w, "analytics ingest unavailable", http.StatusServiceUnavailable)
		return
	}
	proxy.ServeHTTP(w, r)
}

// ingestProxies caches one reverse proxy per endpoint. Keyed by host rather
// than by Server so tests that stand up several servers against several
// upstreams each get their own, and a telemetry firehose does not re-parse a
// URL and rebuild a proxy on every request.
var ingestProxies sync.Map

func ingestProxy(host string) (http.Handler, error) {
	if cached, ok := ingestProxies.Load(host); ok {
		return cached.(http.Handler), nil
	}
	proxy, err := analytics.IngestProxy(host)
	if err != nil {
		return nil, err
	}
	cached, _ := ingestProxies.LoadOrStore(host, proxy)
	return cached.(http.Handler), nil
}
