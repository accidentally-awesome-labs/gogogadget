// Package web wires HTTP routing and middleware.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg config.Config
	log *slog.Logger
	db  *pgxpool.Pool
	// Active-announcement cache: one banner at a time, 30s TTL, refreshed
	// eagerly by invalidateAnnouncementCache() on every admin mutation.
	annMu         sync.Mutex
	ann           *sqlc.Announcement
	annExpires    time.Time
	q             *sqlc.Queries
	version       string
	docs          *content.Docs // embedded markdown; versions with the binary
	types         *content.Registry
	cms           *content.CMS
	verifier      identity.Verifier
	fetcher       identity.UserFetcher
	deleter       identity.Deleter // nil → local-only account deletion
	billingClient billing.Client   // nil when Polar is unconfigured
	analytics     analytics.Capturer
	store         storage.Store // DevStore by default; R2 when configured
	llm           llm.Completer // nil when unconfigured → 503
	flags         flags.Evaluator
	reporter      observability.Reporter

	mux *http.ServeMux

	// metrics is the process-local Prometheus registry (see metrics.go).
	metrics metricsRegistry
}

// Deps is the server wiring bag: every external service enters here, behind
// its seam interface.
type Deps struct {
	Config  *config.Config
	Log     *slog.Logger
	DB      *pgxpool.Pool
	Queries *sqlc.Queries
	Version string
	Docs    *content.Docs
	// ContentTypes declares every content collection. nil → DefaultTypes()
	// (blog posts and changelog releases). Appending one Type is all it takes
	// to add a collection: no migration, no table, no handler, no template.
	ContentTypes []content.Type

	Verifier        identity.Verifier
	Fetcher         identity.UserFetcher
	IdentityDeleter identity.Deleter // nil → local-only deletion; DevDeleter under bypass
	Billing         billing.Client
	Analytics       analytics.Capturer
	Storage         storage.Store          // nil → DevStore(tmp/uploads)
	LLM             llm.Completer          // nil → AI routes 503 not_configured
	Flags           flags.Evaluator        // nil → DB-backed evaluator (30s cache)
	Reporter        observability.Reporter // nil → NoopReporter
}

func NewServer(d Deps) (*Server, error) {
	s := &Server{
		cfg:           *d.Config,
		log:           d.Log,
		db:            d.DB,
		q:             d.Queries,
		version:       d.Version,
		docs:          d.Docs,
		verifier:      d.Verifier,
		fetcher:       d.Fetcher,
		deleter:       d.IdentityDeleter,
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
	// A bad content-type declaration is a wiring bug, so it refuses here. There
	// is deliberately no fallback to the defaults: silently serving a different
	// set of collections than the one declared hides the mistake until a reader
	// notices a missing page.
	reg, err := content.NewRegistry(contentTypesOf(d))
	if err != nil {
		return nil, fmt.Errorf("content types: %w", err)
	}
	s.types = reg
	s.cms = content.NewCMS(s.q, s.types)
	if d.Analytics != nil {
		s.analytics = d.Analytics
	}
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// contentTypesOf returns the declared collections, defaulting to the built-in
// blog and changelog.
func contentTypesOf(d Deps) []content.Type {
	if d.ContentTypes == nil {
		return content.DefaultTypes()
	}
	return d.ContentTypes
}

// currentAnnouncement returns the active platform announcement, cached for
// 30s (DBEvaluator pattern). On a lookup error it keeps the last cached
// value — a hiccup must never blank (or duplicate) the banner; the TTL is
// left expired so the next request retries immediately.
func (s *Server) currentAnnouncement(ctx context.Context) *sqlc.Announcement {
	s.annMu.Lock()
	defer s.annMu.Unlock()
	if time.Now().Before(s.annExpires) {
		return s.ann
	}
	row, err := s.q.GetActiveAnnouncement(ctx)
	switch {
	case err == nil:
		s.ann = &row
	case errors.Is(err, pgx.ErrNoRows):
		s.ann = nil
	default:
		s.log.Error("announcement lookup failed", "error", err)
		return s.ann // stale-if-error; expires stays in the past → retry next request
	}
	s.annExpires = time.Now().Add(30 * time.Second)
	return s.ann
}

// invalidateAnnouncementCache makes the next render re-read the active row
// (called after every admin announcement mutation).
func (s *Server) invalidateAnnouncementCache() {
	s.annMu.Lock()
	defer s.annMu.Unlock()
	s.annExpires = time.Time{}
}

// Handler applies the global middleware stack. The order is load-bearing —
// see docs/architecture: recover → requestID → accessLog → i18n.Detect →
// maintenanceMode → rateLimit → secureHeaders → sessionLoad (identity step) →
// csrf → routes.
func (s *Server) Handler() http.Handler {
	h := http.Handler(s.mux)
	h = s.csrf(h)
	h = s.sessionLoad(h) // Clerk claims, optional; absent cookie → unauthenticated
	h = s.secureHeaders(h)
	h = s.rateLimit(h)
	h = s.maintenanceMode(h) // 503 everything (except probes/static) when on
	h = i18n.Detect(h)       // locale resolution: ?lang= → cookie → Accept-Language
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
