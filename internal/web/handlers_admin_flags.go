package web

import (
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/flags — feature-flag table (admin only via adminChain).
func (s *Server) handleAdminFlags(w http.ResponseWriter, r *http.Request) {
	flags, err := s.q.ListFeatureFlags(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Feature flags", Layout: templates.LayoutAdmin}, templates.AdminFlagsPage(flags))
}

// POST /admin/flags/{key}/toggle
func (s *Server) handleAdminFlagToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	key := r.PathValue("key")
	f, err := s.q.GetFeatureFlag(ctx, key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.SetFeatureFlagEnabled(ctx, sqlc.SetFeatureFlagEnabledParams{Key: key, Enabled: !f.Enabled}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, "", user.ClerkUserID, "flag.updated", map[string]any{"key": key, "enabled": !f.Enabled})
	s.handleAdminFlags(w, r)
}

// POST /admin/flags/{key}/rollout {rollout 0–100}
func (s *Server) handleAdminFlagRollout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	key := r.PathValue("key")
	if _, err := s.q.GetFeatureFlag(ctx, key); err != nil {
		http.NotFound(w, r)
		return
	}
	rollout, err := strconv.Atoi(r.FormValue("rollout"))
	if err != nil || rollout < 0 || rollout > 100 {
		http.Error(w, "rollout must be 0–100", http.StatusUnprocessableEntity)
		return
	}
	if err := s.q.SetFeatureFlagRollout(ctx, sqlc.SetFeatureFlagRolloutParams{Key: key, Rollout: int32(rollout)}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, "", user.ClerkUserID, "flag.updated", map[string]any{"key": key, "rollout": rollout})
	s.handleAdminFlags(w, r)
}
