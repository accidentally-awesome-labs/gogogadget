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
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/justinas/nosurf"
	"golang.org/x/time/rate"
)

// Middleware chain (outermost → innermost), assembled in Handler:
//
//	recover → requestID → accessLog → rateLimit → secureHeaders → sessionLoad → csrf → routes
//
// The order is load-bearing. sessionLoad lands in the identity step, between
// secureHeaders and csrf.

// recover converts panics into the 500 page. Outside production it includes
// the panic detail + stack (agent- and human-actionable); production renders a
// generic page (and reports to Sentry once observability is wired).
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				stack := debug.Stack()
				s.log.Error("panic recovered", "error", rec, "path", r.URL.Path, "request_id", requestIDFrom(r.Context()))
				s.capturePanic(r, rec) // no-op until Sentry is configured
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

// capturePanic reports to Sentry when enabled.
func (s *Server) capturePanic(r *http.Request, rec any) {
	if !s.cfg.SentryEnabled() {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("path", r.URL.Path)
		scope.SetContext("request", sentry.Context{"id": requestIDFrom(r.Context())})
		sentry.CaptureException(fmt.Errorf("panic: %v", rec))
	})
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

// rateLimit sheds load per client IP: 100 req/min, burst 200, on everything
// except /static/*, /healthz, /ingest/*.
//
// NOTE: single-node only, by design. The limiter is in-process; when scaling
// horizontally (fly scale count > 1), swap this for a shared store
// (e.g. Upstash) — that is the documented upgrade trigger.
func (s *Server) rateLimit(next http.Handler) http.Handler {
	rl := newIPRateLimiter(rate.Every(time.Minute/100), 200)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/ingest/") || p == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !rl.allow(clientIP(r)) {
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

type ipEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*ipEntry
	rate    rate.Limit
	burst   int
}

func newIPRateLimiter(r rate.Limit, burst int) *ipRateLimiter {
	rl := &ipRateLimiter{entries: make(map[string]*ipEntry), rate: r, burst: burst}
	go rl.janitor()
	return rl
}

func (rl *ipRateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	e, ok := rl.entries[ip]
	if !ok {
		e = &ipEntry{limiter: rate.NewLimiter(rl.rate, rl.burst)}
		rl.entries[ip] = e
	}
	e.lastSeen = time.Now()
	rl.mu.Unlock()
	return e.limiter.Allow()
}

// janitor sweeps entries idle for >10 minutes so the map cannot grow forever.
func (rl *ipRateLimiter) janitor() {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for range tick.C {
		cutoff := time.Now().Add(-10 * time.Minute)
		rl.mu.Lock()
		for ip, e := range rl.entries {
			if e.lastSeen.Before(cutoff) {
				delete(rl.entries, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// secureHeaders sets the strict header set. CSP is assembled from config so
// the Clerk Frontend API origin lands in connect-src (vendored clerk-js calls
// it to keep the ~60s __session JWT fresh).
func (s *Server) secureHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: https://img.clerk.com",
		"font-src 'self'",
		"connect-src 'self' " + s.cfg.ClerkFrontendAPIURL,
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if s.cfg.Production() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// csrf wraps nosurf. Webhook/API/ingest/health/static paths are exempt —
// webhooks are signature-verified, the API is cookieless Bearer.
//
// Cookie name differs by environment on purpose: production uses the
// __Host- prefix (requires Secure); a __Host- cookie without Secure is
// REJECTED by Safari, which would silently break non-localhost dev.
func (s *Server) csrf(next http.Handler) http.Handler {
	ns := nosurf.New(next)
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
	ns.ExemptRegexps(
		`^/webhooks/.*`,
		`^/api/.*`,
		`^/ingest/.*`,
		`^/static/.*`,
	)
	ns.ExemptPaths("/healthz", "/readyz")
	return ns
}

func csrfCookieName(production bool) string {
	if production {
		return "__Host-csrf"
	}
	return "csrf_token"
}
