// Package web wires HTTP routing and middleware.
package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg     config.Config
	log     *slog.Logger
	db      *pgxpool.Pool
	q       *sqlc.Queries
	version string
	blog    *content.Blog
	docs    *content.Docs
	verifier      identity.Verifier
	fetcher       identity.UserFetcher
	billingClient billing.Client // nil when Polar is unconfigured
	analytics     analytics.Capturer
	store         storage.Store  // DevStore by default; R2 when configured
	llm           llm.Completer // nil when unconfigured → 503
	flags         flags.Evaluator
	reporter      observability.Reporter

	mux *http.ServeMux
}

// Deps is the server wiring bag: every external service enters here, behind
// its seam interface.
type Deps struct {
	Config  config.Config
	Log     *slog.Logger
	DB      *pgxpool.Pool
	Queries *sqlc.Queries
	Version string
	Blog    *content.Blog
	Docs    *content.Docs

	Verifier  identity.Verifier
	Fetcher   identity.UserFetcher
	Billing   billing.Client
	Analytics analytics.Capturer
	Storage   storage.Store // nil → DevStore(tmp/uploads)
	LLM       llm.Completer // nil → AI routes 503 not_configured
	Flags     flags.Evaluator // nil → DB-backed evaluator (30s cache)
	Reporter  observability.Reporter // nil → NoopReporter
}

func NewServer(d Deps) *Server {
	s := &Server{
		cfg:           d.Config,
		log:           d.Log,
		db:            d.DB,
		q:             d.Queries,
		version:       d.Version,
		blog:          d.Blog,
		docs:          d.Docs,
		verifier:      d.Verifier,
		fetcher:       d.Fetcher,
		billingClient: d.Billing,
		analytics:     analytics.NoopCapturer{},
		store:         d.Storage,
		llm:           d.LLM,
		flags:         d.Flags,
		reporter:      d.Reporter,
		mux:           http.NewServeMux(),
	}
	if s.store == nil {
		s.store = storage.NewDevStore("tmp/uploads")
	}
	if s.flags == nil {
		s.flags = flags.NewDBEvaluator(s.q, 30*time.Second)
	}
	if s.reporter == nil {
		s.reporter = observability.NoopReporter{}
	}
	if d.Analytics != nil {
		s.analytics = d.Analytics
	}
	s.routes()
	return s
}

// Handler applies the global middleware stack. The order is load-bearing —
// see docs/architecture: recover → requestID → accessLog → i18n.Detect →
// rateLimit → secureHeaders → sessionLoad (identity step) → csrf → routes.
func (s *Server) Handler() http.Handler {
	h := http.Handler(s.mux)
	h = s.csrf(h)
	h = s.sessionLoad(h) // Clerk claims, optional; absent cookie → unauthenticated
	h = s.secureHeaders(h)
	h = s.rateLimit(h)
	h = i18n.Detect(h) // locale resolution: ?lang= → cookie → Accept-Language
	h = s.accessLog(h)
	h = s.requestID(h)
	h = s.recover(h)
	return maxBytes(h, 10<<20) // 10 MB request cap on every route
}

func maxBytes(next http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
}

// GET /healthz — liveness. Never touches the database.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": s.version})
}

// GET /readyz — readiness. 200 only when a DB ping succeeds.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db not configured"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
