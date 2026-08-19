package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/webhooks"
)

// GET /app/settings/webhooks — endpoints + delivery log. Feature-flagged:
// off for this org → 404 (the tab is hidden too, via Render's ctx gate).
// The full page MUST carry #webhooks-container — the create/toggle/replay
// fragments swap it, and a missing target silently swaps the FORM instead.
func (s *Server) handleSettingsWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksEnabled(r) {
		s.handleNotFound(w, r)
		return
	}
	d, err := s.webhooksData(r)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Webhooks", Layout: templates.LayoutApp}, templates.WebhooksPage(d))
}

// webhooksEnabled is the feature gate for every /app/settings/webhooks* route.
func (s *Server) webhooksEnabled(r *http.Request) bool {
	org := identity.OrgFrom(r.Context())
	if org == nil {
		return false
	}
	return s.flags.Enabled(r.Context(), org.ClerkOrgID, "webhooks")
}

// webhooksData loads the section view model.
func (s *Server) webhooksData(r *http.Request) (templates.WebhooksData, error) {
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	endpoints, err := s.q.ListWebhookEndpointsByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		return templates.WebhooksData{}, err
	}
	deliveries, err := s.q.ListDeliveriesByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		return templates.WebhooksData{}, err
	}
	return templates.WebhooksData{
		Endpoints: endpoints, Deliveries: deliveries, EventTypes: webhooks.EventTypes,
	}, nil
}

// renderWebhooksSection re-renders the fragment (create/toggle/replay).
func (s *Server) renderWebhooksSection(w http.ResponseWriter, r *http.Request, d templates.WebhooksData) {
	fresh, err := s.webhooksData(r)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	fresh.NewSecret = d.NewSecret
	fresh.Rotated = d.Rotated
	fresh.URLErr = d.URLErr
	s.Render(w, r, Page{Title: "Webhooks", Layout: templates.LayoutApp}, templates.WebhooksSection(fresh))
}

// POST /app/settings/webhooks/endpoints — create. URL must be https (the
// SSRF guard rejects non-public hosts at DELIVERY time; the form only
// validates the scheme). Secret is generated here and shown exactly once.
func (s *Server) handleWebhookEndpointCreate(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksEnabled(r) {
		s.handleNotFound(w, r)
		return
	}
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)

	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	description := strings.TrimSpace(r.FormValue("description"))

	if !strings.HasPrefix(rawURL, "https://") {
		w.WriteHeader(http.StatusUnprocessableEntity)
		s.renderWebhooksSection(w, r, templates.WebhooksData{URLErr: "URL must start with https://"})
		return
	}

	secret := webhooks.NewSecret()
	eventTypes := r.Form["event_types"]
	if eventTypes == nil {
		eventTypes = []string{} // zero checked = all events; nil would encode NULL → NOT NULL violation
	}
	ep, err := s.q.InsertWebhookEndpoint(ctx, sqlc.InsertWebhookEndpointParams{
		ClerkOrgID: org.ClerkOrgID, CreatedBy: user.ClerkUserID,
		Url: rawURL, Secret: secret,
		EventTypes: eventTypes, Description: description,
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "webhook_endpoint.created", map[string]any{"id": ep.ID, "url": ep.Url})
	Toast(w, "success", "Endpoint created")
	s.renderWebhooksSection(w, r, templates.WebhooksData{NewSecret: secret})
}

// POST /app/settings/webhooks/endpoints/{id}/disable|enable
func (s *Server) handleWebhookEndpointToggle(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksEnabled(r) {
		s.handleNotFound(w, r)
		return
	}
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	disable := strings.HasSuffix(r.URL.Path, "/disable")
	ep, err := s.q.GetWebhookEndpoint(ctx, sqlc.GetWebhookEndpointParams{ID: id, ClerkOrgID: org.ClerkOrgID})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.SetWebhookEndpointDisabled(ctx, sqlc.SetWebhookEndpointDisabledParams{
		ID: id, Disabled: disable, ClerkOrgID: org.ClerkOrgID,
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	action := "webhook_endpoint.enabled"
	if disable {
		action = "webhook_endpoint.disabled"
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, action, map[string]any{"id": ep.ID})
	s.renderWebhooksSection(w, r, templates.WebhooksData{})
}

// POST /app/settings/webhooks/deliveries/{id}/replay — reset to pending and
// re-enqueue the delivery job.
func (s *Server) handleWebhookDeliveryReplay(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksEnabled(r) {
		s.handleNotFound(w, r)
		return
	}
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if err := s.q.ResetWebhookDelivery(ctx, sqlc.ResetWebhookDeliveryParams{ID: id, ClerkOrgID: org.ClerkOrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	if err := jobs.Enqueue(ctx, s.q, jobs.KindWebhookDeliver, jobs.WebhookDeliverPayload{DeliveryID: id}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "webhook_delivery.replayed", map[string]any{"id": id})
	Toast(w, "success", "Delivery requeued")
	s.renderWebhooksSection(w, r, templates.WebhooksData{})
}

// POST /app/settings/webhooks/endpoints/{id}/rotate — mint a new signing
// secret. The old one keeps verifying for the grace window
// (jobs.WebhookRotationGrace) so receivers can roll over without dropping
// deliveries; the janitor clears it afterwards. Shown exactly once, like
// creation.
func (s *Server) handleWebhookEndpointRotate(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksEnabled(r) {
		s.handleNotFound(w, r)
		return
	}
	ctx := r.Context()
	org := identity.OrgFrom(ctx)
	user := identity.UserFrom(ctx)

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	secret := webhooks.NewSecret()
	ep, err := s.q.RotateWebhookEndpointSecret(ctx, sqlc.RotateWebhookEndpointSecretParams{
		ID: id, ClerkOrgID: org.ClerkOrgID, Secret: secret,
	})
	if err != nil {
		// Cross-org ids simply do not match the WHERE clause.
		http.NotFound(w, r)
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "webhook_endpoint.secret_rotated",
		map[string]any{"id": ep.ID, "url": ep.Url})
	Toast(w, "success", i18n.T(ctx, "webhooks.rotated"))
	s.renderWebhooksSection(w, r, templates.WebhooksData{NewSecret: secret, Rotated: true})
}
