// Adapters that let the generated route table name a Server method for surfaces
// whose handler is a value rather than a method, plus the predicates that gate
// conditional routes. Keeping these here means the generated table has exactly
// one shape and never special-cases a handler.
package web

import (
	"net/http"
	"net/http/pprof"
)

// serveStaticAssets adapts the embedded-asset handler, which is built once per
// server rather than per request.
func (s *Server) serveStaticAssets(w http.ResponseWriter, r *http.Request) {
	s.staticAssets.ServeHTTP(w, r)
}

// metricsEnabled gates /metrics. Production without a token leaves the route
// unregistered rather than answering 401: internal Go statistics must not be
// public by default, and an unregistered path does not advertise itself.
func (s *Server) metricsEnabled() bool {
	return !s.cfg.Production() || s.cfg.MetricsToken != ""
}

// debugProfilingEnabled gates the pprof surface. It is never registered in
// production, so the profiler cannot be reached there at all.
func (s *Server) debugProfilingEnabled() bool { return !s.cfg.Production() }

func (s *Server) handlePprofIndex(w http.ResponseWriter, r *http.Request)   { pprof.Index(w, r) }
func (s *Server) handlePprofCmdline(w http.ResponseWriter, r *http.Request) { pprof.Cmdline(w, r) }
func (s *Server) handlePprofProfile(w http.ResponseWriter, r *http.Request) { pprof.Profile(w, r) }
func (s *Server) handlePprofSymbol(w http.ResponseWriter, r *http.Request)  { pprof.Symbol(w, r) }
func (s *Server) handlePprofTrace(w http.ResponseWriter, r *http.Request)   { pprof.Trace(w, r) }
