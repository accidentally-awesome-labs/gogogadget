// Package web wires HTTP routing and middleware.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/billing"
	"github.com/gogogadget/gogogadget/internal/cache"
	"github.com/gogogadget/gogogadget/internal/config"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/flags"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	identitysession "github.com/gogogadget/gogogadget/internal/identity/session"
	"github.com/gogogadget/gogogadget/internal/llm"
	"github.com/gogogadget/gogogadget/internal/observability"
	"github.com/gogogadget/gogogadget/internal/ratelimit"
	"github.com/gogogadget/gogogadget/internal/realtime"
	"github.com/gogogadget/gogogadget/internal/search"
	"github.com/gogogadget/gogogadget/internal/storage"
	"github.com/gogogadget/gogogadget/internal/telemetry"
	"github.com/gogogadget/gogogadget/internal/usage"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Server struct {
	cfg config.Config
	log *slog.Logger
	db  *pgxpool.Pool
	// Active-announcement cache: one banner at a time, 30s TTL, refreshed
	// eagerly by invalidateAnnouncementCache() on every admin mutation.
	annMu           sync.Mutex
	ann             *sqlc.Announcement
	annExpires      time.Time
	q               *sqlc.Queries
	version         string
	docs            *content.Docs // embedded markdown; versions with the binary
	types           *content.Registry
	cms             *content.CMS
	verifier        identity.Verifier
	sessionLoader   identitysession.Loader
	fetcher         identity.UserFetcher
	deleter         identity.Deleter
	navigator       identity.Navigator
	billingClient   billing.Client
	billingCatalog  billing.PlanCatalog
	analytics       analytics.Capturer
	store           storage.Store
	llm             llm.Completer
	flags           flags.Service
	reporter        observability.Reporter
	realtime        realtime.Broker
	limiter         ratelimit.Limiter
	telemetry       telemetry.Providers
	identityWebhook identity.Webhook
	billingWebhook  billing.BillingWebhook
	webhookEmitter  webhooks.Emitter
	usageRecorder   usage.Recorder
	searchIndex     search.Index
	auditExporter   audit.Exporter
	mux             *http.ServeMux

	// metrics is the process-local Prometheus registry (see metrics.go).
	metrics metricsRegistry

	// testOnlyModules mirrors Deps.TestOnlyModules; see that field.
	testOnlyModules bool

	// api is the /api/v1 transport, composed once (see api_transport.go).
	api apiSurface

	// staticAssets is the embedded-asset handler, built once at construction.
	staticAssets http.Handler

	// policies resolves a request to the policy its route declared.
	policies *policyMatcher
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
	Cache        cache.Store
	// TestOnlyModules enables surfaces owned by test-only modules under
	// registry/testdata. web.NewModule — the constructor the generated bootstrap
	// calls — never sets it, so a booted production runtime cannot reach them.
	TestOnlyModules bool

	Verifier          identity.Verifier
	SessionLoader     identitysession.Loader
	Fetcher           identity.UserFetcher
	IdentityDeleter   identity.Deleter
	IdentityNavigator identity.Navigator
	Billing           billing.Client
	BillingCatalog    billing.PlanCatalog
	Analytics         analytics.Capturer
	Storage           storage.Store
	LLM               llm.Completer
	Flags             flags.Service
	Reporter          observability.Reporter
	Realtime          realtime.Broker
	RateLimiter       ratelimit.Limiter
	Telemetry         telemetry.Providers
	IdentityWebhook   identity.Webhook
	BillingWebhook    billing.BillingWebhook
	WebhookEmitter    webhooks.Emitter
	UsageRecorder     usage.Recorder
	SearchIndex       search.Index
	AuditExporter     audit.Exporter
}

func NewServer(d Deps) (*Server, error) {
	if d.Config == nil {
		return nil, errors.New("web: config capability is required")
	}
	if _, err := content.NewRegistry(contentTypesOf(d)); err != nil {
		return nil, fmt.Errorf("content types: %w", err)
	}
	for name, value := range map[string]any{
		"identity.verifier": d.Verifier, "identity.fetcher": d.Fetcher,
		"identity.deleter": d.IdentityDeleter, "identity.navigator": d.IdentityNavigator,
		"identity.webhook": d.IdentityWebhook, "billing.client": d.Billing,
		"billing.catalog": d.BillingCatalog, "billing.webhook": d.BillingWebhook,
		"identity.session": d.SessionLoader,
		"storage.store":    d.Storage, "flags.evaluator": d.Flags,
		"observability.reporter": d.Reporter, "analytics.capturer": d.Analytics,
		"llm.completer": d.LLM, "realtime.broker": d.Realtime,
		"rate_limit.limiter": d.RateLimiter,
	} {
		if value == nil {
			return nil, fmt.Errorf("web: required capability %s is missing", name)
		}
	}
	s := &Server{
		cfg: *d.Config, log: d.Log, db: d.DB, q: d.Queries, version: d.Version,
		testOnlyModules: d.TestOnlyModules, docs: d.Docs, verifier: d.Verifier,
		sessionLoader: d.SessionLoader, identityWebhook: d.IdentityWebhook,
		billingWebhook: d.BillingWebhook, fetcher: d.Fetcher,
		deleter: d.IdentityDeleter, navigator: d.IdentityNavigator,
		billingClient: d.Billing, billingCatalog: d.BillingCatalog,
		analytics: d.Analytics, store: d.Storage, llm: d.LLM, flags: d.Flags,
		reporter: d.Reporter, realtime: d.Realtime, limiter: d.RateLimiter, telemetry: d.Telemetry, webhookEmitter: d.WebhookEmitter, usageRecorder: d.UsageRecorder, auditExporter: d.AuditExporter, searchIndex: d.SearchIndex, mux: http.NewServeMux(),
	}
	reg, _ := content.NewRegistry(contentTypesOf(d))
	s.types = reg
	s.cms = content.NewCMSWithCache(s.q, s.types, d.Cache, func(ctx context.Context, err error) { s.reporter.Capture(err) })
	s.api = newAPISurface(s)
	s.staticAssets = s.serveStatic()
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// contentTypesOf returns the declared collections, defaulting to the built-in
// blog and changelog.
func contentTypesOf(d Deps) []content.Type {
	types := d.ContentTypes
	if types == nil {
		types = content.DefaultTypes()
	}
	if d.TestOnlyModules {
		// Test-only modules contribute their collections here so the fixture
		// travels the same path a shipped module does.
		types = append(append([]content.Type{}, types...), testOnlyContentTypes()...)
	}
	return types
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
// see docs/architecture: maxBytes → recover → routeBodyLimit → requestID →
// accessLog → i18n.Detect → maintenanceMode → rateLimit → secureHeaders →
// sessionLoad (identity step) → csrf → routes.
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
	h = s.routeBodyLimit(h) // per-route declared cap, tighter than the global one
	nextHandler := telemetry.HTTP(h, s.telemetry, "web")
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := r.WithContext(templates.WithProviderEnvironment(r.Context(), s.cfg.Env))
		nextHandler.ServeHTTP(w, next)
	})
	return maxBytes(h, globalMaxBodyBytes) // global request cap on every route
}

// globalMaxBodyBytes is the cap every route gets. A route may declare a tighter
// one; none may raise it.
const globalMaxBodyBytes int64 = 10 << 20

// routeBodyLimit applies the cap the matched route declared. Without this,
// RoutePolicy.MaxBodyBytes was generated, validated, and never read: the
// webhook receivers declare 1 MiB and would still let io.ReadAll buffer 10 MB
// before signature verification had a chance to reject it.
//
// It runs outside csrf on purpose — csrf parses the form, and parsing reads the
// body — and it narrows rather than replaces the global cap, so a policy value
// above globalMaxBodyBytes cannot widen anything.
func (s *Server) routeBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy, declared := s.policies.policyFor(r)
		if declared && policy.MaxBodyBytes > 0 && policy.MaxBodyBytes < globalMaxBodyBytes {
			r.Body = http.MaxBytesReader(w, r.Body, policy.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
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

func (s *Server) serveRealtime(w http.ResponseWriter, r *http.Request) {
	if s.realtime == nil {
		http.Error(w, "realtime_unavailable", http.StatusServiceUnavailable)
		return
	}
	topic := strings.TrimPrefix(r.URL.Path, "/events/")
	if topic == "" || strings.ContainsAny(topic, "\r\n") {
		http.Error(w, "invalid realtime topic", http.StatusBadRequest)
		return
	}
	sub, err := s.realtime.Subscribe(r.Context(), topic)
	if err != nil {
		s.log.ErrorContext(r.Context(), "realtime subscribe failed", "topic", topic, "error", err)
		http.Error(w, "realtime_unavailable", http.StatusServiceUnavailable)
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	for {
		payload, err := sub.Next(r.Context())
		if err != nil {
			if r.Context().Err() == nil {
				s.log.ErrorContext(r.Context(), "realtime stream closed", "topic", topic, "error", err)
			}
			return
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) emitWebhook(ctx context.Context, orgID, eventType string, data any) {
	if s.webhookEmitter != nil {
		if err := s.webhookEmitter.Emit(ctx, orgID, eventType, data); err != nil {
			s.reporter.Capture(err)
		}
		return
	}
	webhooks.Emit(ctx, s.q, orgID, eventType, data)
}

func (s *Server) logAudit(ctx context.Context, orgID, userID, action string, metadata map[string]any) {
	audit.Log(ctx, s.q, orgID, userID, action, metadata)
	if s.auditExporter != nil {
		_ = s.auditExporter.Export(ctx, audit.Entry{OrgID: orgID, UserID: userID, Action: action, Metadata: metadata})
	}
}

func (s *Server) indexProject(ctx context.Context, orgID, id, name, status string) {
	if s.searchIndex == nil {
		return
	}
	_ = s.searchIndex.Upsert(ctx, search.Document{TenantID: orgID, Collection: "projects", ID: id, Text: name, Fields: map[string]string{"status": status}})
}
func (s *Server) deleteProjectIndex(ctx context.Context, orgID, id string) {
	if s.searchIndex != nil {
		_ = s.searchIndex.Delete(ctx, orgID, "projects", id)
	}
}
