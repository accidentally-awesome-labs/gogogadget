package web

import (
	"net/http"
	"net/http/pprof"

	"github.com/gogogadget/gogogadget/internal/analytics"
	"github.com/gogogadget/gogogadget/internal/api"
)

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	// /metrics: bearer-gated when METRICS_TOKEN is set; unregistered in
	// production without one (internal Go stats must not be public default).
	if !s.cfg.Production() || s.cfg.MetricsToken != "" {
		s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	}
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)

	s.mux.Handle("GET /static/", s.serveStatic())
	s.mux.HandleFunc("GET /favicon.ico", s.faviconRedirect)

	// Public marketing pages.
	s.mux.HandleFunc("GET /{$}", s.handleHome)
	s.mux.HandleFunc("GET /pricing", s.handlePricing)
	s.mux.HandleFunc("GET /terms", s.handleTerms)
	s.mux.HandleFunc("GET /privacy", s.handlePrivacy)

	// Content: blog, feed, SEO.
	s.mux.HandleFunc("GET /blog", s.handleBlogIndex)
	s.mux.HandleFunc("GET /blog/{slug}", s.handleBlogPost)
	s.mux.HandleFunc("GET /rss.xml", s.handleRSS)
	s.mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	s.mux.HandleFunc("GET /robots.txt", s.handleRobots)

	// Docs (in-app, versioned with the code).
	s.mux.HandleFunc("GET /docs", s.handleDocsIndex)
	s.mux.HandleFunc("GET /docs/{slug}", s.handleDocsPage)
	// Exact literal beats the {slug} wildcard per mux precedence rules.
	s.mux.HandleFunc("GET /docs/search", s.handleDocsSearch)

	// Auth redirects (public) → Clerk hosted Account Portal.
	s.mux.HandleFunc("GET /login", s.handleLogin)
	s.mux.HandleFunc("GET /signup", s.handleSignup)
	s.mux.HandleFunc("GET /logout", s.handleLogout)

	// Zero-account dev mode (DEV_AUTH_BYPASS is boot-refused in production).
	if s.cfg.DevAuthBypass {
		s.mux.HandleFunc("GET /dev/login", s.handleDevLogin)
		s.mux.HandleFunc("GET /dev/switch-org", s.handleDevSwitchOrg)
	}

	// Locale switch (public; NOT CSRF-exempt — switcher forms carry the token
	// via the body-inherited X-CSRF-Token header).
	s.mux.HandleFunc("POST /set-locale", s.handleSetLocale)

	// Webhooks are CSRF-exempt in the chain and signature-verified here.
	s.mux.HandleFunc("POST /webhooks/clerk", s.handleClerkWebhook)
	s.mux.HandleFunc("POST /webhooks/polar", s.handlePolarWebhook)

	// Guarded app group: RequireAuth → RequireNotDisabled → RequireOrg → LoadPlan.
	appMux := http.NewServeMux()
	appMux.HandleFunc("GET /app", s.handleDashboard)
	appMux.HandleFunc("GET /app/projects", s.handleProjects)
	appMux.HandleFunc("POST /app/projects/export", s.handleProjectsExport)
	appMux.HandleFunc("GET /app/projects/new", s.handleProjectNew)
	appMux.HandleFunc("POST /app/projects", s.handleProjectCreate)
	appMux.HandleFunc("GET /app/projects/{id}/edit", s.handleProjectEdit)
	appMux.HandleFunc("POST /app/projects/{id}", s.handleProjectUpdate)
	appMux.HandleFunc("POST /app/projects/{id}/archive", s.handleProjectArchive)
	appMux.HandleFunc("DELETE /app/projects/{id}", s.handleProjectDelete)
	appMux.HandleFunc("GET /app/files", s.handleFiles)
	appMux.HandleFunc("POST /app/files", s.handleFileUpload)
	appMux.HandleFunc("GET /app/files/{id}", s.handleFileDownload)
	appMux.HandleFunc("DELETE /app/files/{id}", s.handleFileDelete)

	appMux.HandleFunc("GET /app/settings/account", s.handleSettingsAccount)
	appMux.HandleFunc("GET /app/settings/org", s.handleSettingsOrg)
	appMux.HandleFunc("GET /app/settings/billing", s.handleSettingsBilling)
	appMux.HandleFunc("GET /app/settings/billing/fragment", s.handleSettingsBillingFragment)
	appMux.HandleFunc("POST /app/billing/checkout", s.handleBillingCheckout)
	appMux.HandleFunc("POST /app/billing/portal", s.handleBillingPortal)
	appMux.HandleFunc("GET /app/settings/api", s.handleSettingsAPI)
	appMux.HandleFunc("POST /app/settings/api/tokens", s.handleAPITokenCreate)
	appMux.HandleFunc("DELETE /app/settings/api/tokens/{id}", s.handleAPITokenRevoke)
	appMux.HandleFunc("GET /app/settings/webhooks", s.handleSettingsWebhooks)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints", s.handleWebhookEndpointCreate)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/disable", s.handleWebhookEndpointToggle)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/enable", s.handleWebhookEndpointToggle)
	appMux.HandleFunc("POST /app/settings/webhooks/endpoints/{id}/rotate", s.handleWebhookEndpointRotate)
	appMux.HandleFunc("POST /app/settings/webhooks/deliveries/{id}/replay", s.handleWebhookDeliveryReplay)
	appMux.HandleFunc("GET /app/activity", s.handleActivity)
	appMux.HandleFunc("POST /app/impersonation/exit", s.handleImpersonationExit)
	appMux.HandleFunc("GET /app/notifications", s.handleNotifications)
	appMux.HandleFunc("GET /app/settings/notifications", s.handleSettingsNotifications)
	appMux.HandleFunc("POST /app/settings/notifications", s.handleSettingsNotificationsSave)
	appMux.HandleFunc("GET /app/settings/account/export", s.handleAccountExport)
	appMux.HandleFunc("POST /app/settings/account/delete", s.handleAccountDelete)
	appMux.HandleFunc("GET /app/notifications/badge", s.handleNotificationsBadge)
	appMux.HandleFunc("GET /app/notifications/stream", s.handleNotificationsStream)
	appMux.HandleFunc("POST /app/notifications/{id}/read", s.handleNotificationRead)
	appMux.HandleFunc("POST /app/notifications/read-all", s.handleNotificationsReadAll)
	s.mux.Handle("/app", s.appChain(appMux))
	s.mux.Handle("/app/", s.appChain(appMux))

	// Admin group: appChain + RequireAdmin.
	adminMux := http.NewServeMux()
	adminMux.HandleFunc("GET /admin", s.handleAdminHome)
	adminMux.HandleFunc("GET /admin/users", s.handleAdminUsers)
	adminMux.HandleFunc("POST /admin/flags", s.handleAdminFlagCreate)
	adminMux.HandleFunc("GET /admin/flags/{key}", s.handleAdminFlagDetail)
	adminMux.HandleFunc("POST /admin/flags/{key}/delete", s.handleAdminFlagDelete)
	adminMux.HandleFunc("POST /admin/flags/{key}/overrides", s.handleAdminFlagOverrideSet)
	adminMux.HandleFunc("POST /admin/flags/{key}/overrides/{org}/delete", s.handleAdminFlagOverrideDelete)
	adminMux.HandleFunc("GET /admin/schedules", s.handleAdminSchedules)
	adminMux.HandleFunc("POST /admin/schedules", s.handleAdminScheduleCreate)
	adminMux.HandleFunc("POST /admin/schedules/{id}/toggle", s.handleAdminScheduleToggle)
	adminMux.HandleFunc("POST /admin/schedules/{id}/run", s.handleAdminScheduleRun)
	adminMux.HandleFunc("POST /admin/schedules/{id}/delete", s.handleAdminScheduleDelete)
	adminMux.HandleFunc("POST /admin/users/{id}/disable", s.handleAdminUserDisable)
	adminMux.HandleFunc("GET /admin/users/{id}/impersonate", s.handleAdminImpersonateForm)
	adminMux.HandleFunc("POST /admin/users/{id}/impersonate", s.handleAdminImpersonate)
	adminMux.HandleFunc("GET /admin/orgs", s.handleAdminOrgs)
	adminMux.HandleFunc("GET /admin/flags", s.handleAdminFlags)
	adminMux.HandleFunc("POST /admin/flags/{key}/toggle", s.handleAdminFlagToggle)
	adminMux.HandleFunc("POST /admin/flags/{key}/rollout", s.handleAdminFlagRollout)
	adminMux.HandleFunc("GET /admin/audit", s.handleAdminAudit)
	adminMux.HandleFunc("GET /admin/jobs", s.handleAdminJobs)
	adminMux.HandleFunc("POST /admin/jobs/{id}/requeue", s.handleAdminJobRequeue)
	adminMux.HandleFunc("GET /admin/announcements", s.handleAdminAnnouncements)
	adminMux.HandleFunc("POST /admin/announcements", s.handleAdminAnnouncementCreate)
	adminMux.HandleFunc("POST /admin/announcements/{id}/activate", s.handleAdminAnnouncementActivate)
	adminMux.HandleFunc("POST /admin/announcements/{id}/deactivate", s.handleAdminAnnouncementDeactivate)
	adminMux.HandleFunc("POST /admin/announcements/{id}/delete", s.handleAdminAnnouncementDelete)
	s.mux.Handle("/admin", s.adminChain(adminMux))
	s.mux.Handle("/admin/", s.adminChain(adminMux))

	// Public JSON API: cookieless Bearer auth (CSRF-exempt in the chain).
	// The contract itself: unauthenticated so tooling can read it before a
	// token exists. Registered before the /api/ catch-all 404.
	s.mux.HandleFunc("GET /api/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(api.OpenAPISpec)
	})

	apiMW := api.NewMiddleware(s.q, s.cfg.APIRateLimitPerMinute)
	apiProjects := &api.Projects{Q: s.q}
	s.mux.Handle("GET /api/v1/projects", apiMW.RequireAPIToken("read", http.HandlerFunc(apiProjects.ListProjects)))
	// Unsafe verbs run inside Idempotent, which sits under RequireAPIToken:
	// the key is scoped to the authenticated org, so it needs identity first.
	s.mux.Handle("POST /api/v1/projects", apiMW.RequireAPIToken("write",
		apiMW.Idempotent(http.HandlerFunc(apiProjects.CreateProject))))
	apiAI := &api.AI{Q: s.q, LLM: s.llm}
	s.mux.Handle("POST /api/v1/ai/chat", apiMW.RequireAPIToken("write",
		apiMW.Idempotent(http.HandlerFunc(apiAI.Chat))))
	// Unknown /api/ routes get the JSON 404, never the HTML one.
	s.mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.WriteError(w, http.StatusNotFound, "not_found", "Unknown API route.")
	}))

	// PostHog reverse proxy (registered only when configured; CSRF- and
	// rate-limit-exempt in the chain).
	if s.cfg.PostHogEnabled() {
		if proxy, err := analytics.IngestProxy(s.cfg.PostHogHost); err == nil {
			s.mux.Handle("/ingest/", proxy)
		} else {
			s.log.Error("posthog proxy", "error", err)
		}
	}

	// pprof outside production only (zero-config profiling).
	if !s.cfg.Production() {
		s.mux.HandleFunc("GET /debug/pprof/", pprof.Index)
		s.mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		s.mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		s.mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		s.mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}

	// Catch-all 404 (least-specific pattern matches last; method-less so it
	// can't conflict with the /app and /admin subtrees).
	s.mux.HandleFunc("/{rest...}", s.handleNotFound)
}
