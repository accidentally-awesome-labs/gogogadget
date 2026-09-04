package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/ratelimit"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
	"github.com/justinas/nosurf"
)

// Middleware chain (outermost → innermost), assembled in Handler:
//
//	maxBytes → recover → routeBodyLimit → requestID → accessLog → i18n.Detect → maintenanceMode → rateLimit → secureHeaders → sessionLoad → csrf → routes
//
// The order is load-bearing. sessionLoad lands in the identity step, between
// secureHeaders and csrf. maintenanceMode sits inside i18n.Detect (the 503
// page needs the locale) but outside rateLimit (shed load before the limiter
// churns). routeBodyLimit sits outside csrf because csrf parses the form, which
// reads the body — a cap applied after that has already been bypassed.
//
// maintenanceMode returns 503 for everything when MAINTENANCE_MODE is on,
// except /healthz, /readyz, /static/, and /favicon.ico (probes + CSS must
// live). /api/ paths get the JSON error shape; pages get the Maintenance
// page. Runs before sessionLoad — no session is available by design.
func (s *Server) maintenanceMode(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.MaintenanceMode {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if policy, declared := s.policies.policyFor(r); declared && policy.MaintenanceExempt {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") {
			api.WriteError(w, http.StatusServiceUnavailable, "maintenance", "Service under maintenance.")
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: i18n.T(r.Context(), "errors.maintenance"), Layout: templates.LayoutPublic}, templates.Maintenance())
	})
}

// recover converts panics into the 500 page. Outside production it includes
// the panic detail + stack (agent- and human-actionable); production renders a
// generic page (and reports through the observability reporter seam).
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				s.log.Error("panic recovered", "error", rec, "path", r.URL.Path, "request_id", requestIDFrom(r.Context()))
				s.capturePanic(r, rec)
				detail := ""
				if !s.cfg.Production() {
					detail = fmt.Sprintf("%v\n\n%s", rec, stack)
				}
				s.renderError(w, r, detail)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// capturePanic reports through the observability seam (NoopReporter when
// Sentry is unconfigured).
func (s *Server) capturePanic(r *http.Request, rec any) {
	s.reporter.CaptureRequest(r, fmt.Errorf("panic: %v", rec))
}

type ctxKeyRequestID struct{}

func requestIDFrom(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID{}).(string); ok {
		return v
	}
	return ""
}

// requestID assigns every request a 16-byte hex id, exposed as X-Request-Id.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b [16]byte
		_, _ = rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID{}, id)))
	})
}

// accessLog emits one slog line per request; 5xx at ERROR, the rest INFO.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.metrics.observe(sw.status, time.Since(start))
		attrs := []any{
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"bytes", sw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestIDFrom(r.Context()),
		}
		if sw.status >= 500 {
			s.log.Error("request", attrs...)
		} else {
			s.log.Info("request", attrs...)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Flush preserves http.Flusher for streaming/SSE through the wrapper.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.NewResponseController reach the real writer — SSE handlers
// need it for SetWriteDeadline(…{}) without dropping the status capture.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// rateLimit sheds load per client IP: RATE_LIMIT_RPM req/min (default 100),
// burst 2×, on everything whose declared RoutePolicy does not exempt it.
//
// NOTE: single-node only, by design. The limiter is in-process; when scaling
// horizontally (fly scale count > 1), swap this for a shared store
// (e.g. Upstash) — that is the documented upgrade trigger.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if policy, declared := s.policies.policyFor(r); declared && policy.RateExempt {
			next.ServeHTTP(w, r)
			return
		}

		var decision ratelimit.Decision
		var err error
		if s.limiter != nil {
			decision, err = s.limiter.Allow(r.Context(), clientIP(r))
			if err != nil {
				s.log.ErrorContext(r.Context(), "rate limit unavailable", "error", err)
				if strings.HasPrefix(r.URL.Path, "/api/") {
					api.WriteError(w, http.StatusServiceUnavailable, "rate_limit_unavailable", "Rate limiting is temporarily unavailable.")
				} else {
					s.renderStatus(w, r, http.StatusServiceUnavailable, "Service unavailable", "Rate limiting is temporarily unavailable.")
				}
				return
			}
		} else {
			s.log.ErrorContext(r.Context(), "rate limit unavailable", "error", "limiter capability is missing")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				api.WriteError(w, http.StatusServiceUnavailable, "rate_limit_unavailable", "Rate limiting is temporarily unavailable.")
			} else {
				s.renderStatus(w, r, http.StatusServiceUnavailable, "Service unavailable", "Rate limiting is temporarily unavailable.")
			}
			return
		}
		if !decision.Allowed {
			w.Header().Set("Retry-After", "1")
			s.renderStatus(w, r, http.StatusTooManyRequests, "Too many requests", "Slow down and try again in a moment.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP prefers Fly-Client-IP (set by the fly.io edge, not spoofable by
// clients there). Off-Fly it falls back to RemoteAddr — README documents that
// a bare proxy then makes this spoofable.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("Fly-Client-IP"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// secureHeaders sets the strict header set. CSP is assembled from config so
// the Clerk Frontend API origin lands in connect-src (vendored clerk-js calls
// it to keep the ~60s __session JWT fresh). The origin is read by key, not by
// field: ggg/system/identity-clerk declares it and derives it at config load,
// and deselecting that adapter must leave this directive as plain 'self'
// rather than breaking the build of a module that never chose Clerk.
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		// clerk-js v5 runs its session handshake inside blob: Web Workers —
		// without this, auth loops forever (reported by clerk-js at integration).
		"worker-src 'self' blob:",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https://img.clerk.com",
		"font-src 'self'",
		"connect-src 'self' " + s.cfg.Value("CLERK_FRONTEND_API_URL"),
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if s.cfg.Production() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// csrf wraps nosurf. Which requests are exempt is decided by the policy each
// route declared, not by a path pattern; see structurallyCSRFExempt for the two
// surfaces that are registered outside the route table by design.
//
// The token is published to the page along two paths, and both are live: an
// inherited request header for htmx (the only path a fragment request has, since
// a button with hx-delete has no form) and a hidden field inside every unsafe
// ui.Form (the only path a submit has when htmx never loaded). This middleware
// owns the second one: it puts the masked token into the request context, so
// ui.Form can render the field without any handler having to remember to pass a
// token in. nosurf has already computed that token by the time next runs, so
// this is a context lookup, not extra work per request.
//
// Cookie name differs by environment on purpose: production uses the
// __Host- prefix (requires Secure); a __Host- cookie without Secure is
// REJECTED by Safari, which would silently break non-localhost dev.
func (s *Server) csrf(next http.Handler) http.Handler {
	ns := nosurf.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(ui.WithCSRFToken(r.Context(), nosurf.Token(r))))
	}))
	ns.SetFailureHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		detail := "Your session expired or the form token was invalid. Go back and try again."
		if !s.cfg.Production() {
			detail += " (reason: " + nosurf.Reason(r).Error() + ")"
		}
		s.renderStatus(w, r, http.StatusForbidden, "Forbidden", detail)
	}))
	ns.SetBaseCookie(http.Cookie{
		Name:     csrfCookieName(s.cfg.Production()),
		Path:     "/",
		Secure:   s.cfg.Production(),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	// nosurf v1.2 enforces same-origin (CVE-2025-46721) and assumes HTTPS by
	// default; over plaintext dev HTTP every form POST would 403. Determine
	// TLS per request: direct TLS, or the edge's X-Forwarded-Proto (fly.io).
	ns.SetIsTLSFunc(func(r *http.Request) bool {
		return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	})
	// Exemptions come from the route that declared them, resolved through the
	// same matching rules that dispatch the request. There is deliberately no
	// prefix or regex list: `^/api/.*` exempts every future path under /api
	// whether or not anyone reasoned about it, and that is how an exemption
	// silently widens.
	ns.ExemptFunc(func(r *http.Request) bool {
		policy, declared := s.policies.policyFor(r)
		if declared {
			return policy.CSRFExempt
		}
		return s.structurallyCSRFExempt(r)
	})
	return ns
}

func csrfCookieName(production bool) string {
	if production {
		return "__Host-csrf"
	}
	return "csrf_token"
}

// structurallyCSRFExempt covers the one surface registered outside the
// generated route table, because the Route contract (one method, one pattern)
// cannot express it: /api/ is the JSON catch-all for unknown API paths. It
// answers 404 in JSON; without this an unknown API POST would get an HTML 403
// from CSRF instead of the API error shape its client can parse.
//
// The analytics proxy used to be the second entry. It is now two declared
// routes owned by the analytics adapter, so its exemption is a csrf_exempt
// with a csrf_reason in the manifest that the policy matcher reads — the same
// place every other exemption lives, and one that leaves with the adapter.
func (s *Server) structurallyCSRFExempt(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/")
}
