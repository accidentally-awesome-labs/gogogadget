package web

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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
	s.invalidateFlagCache()
	audit.Log(ctx, s.q, "", user.UserID, "flag.updated", map[string]any{"key": key, "enabled": !f.Enabled})
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
	s.invalidateFlagCache()
	audit.Log(ctx, s.q, "", user.UserID, "flag.updated", map[string]any{"key": key, "rollout": rollout})
	s.handleAdminFlags(w, r)
}

// invalidateFlagCache drops the evaluator's 30s flag-row cache so admin
// mutations (create/delete/toggle/rollout) take effect on the next render.
func (s *Server) invalidateFlagCache() {
	if inv, ok := s.flags.(interface{ Invalidate() }); ok {
		inv.Invalidate()
	}
}

var flagKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type flagCreateInput struct {
	Key         string `form:"key"`
	Description string `form:"description"`
	Rollout     string `form:"rollout"`
}

// POST /admin/flags — create (key must be unique; enabled starts off).
func (s *Server) handleAdminFlagCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	var input flagCreateInput
	if err := decodeForm(r, &input); err != nil {
		s.renderFlagFormError(w, r, input, i18n.T(ctx, "flags.invalid"))
		return
	}
	input.Key = strings.TrimSpace(input.Key)
	input.Description = strings.TrimSpace(input.Description)
	rollout, err := strconv.Atoi(strings.TrimSpace(input.Rollout))
	if !flagKeyRe.MatchString(input.Key) || len(input.Description) > 200 ||
		err != nil || rollout < 0 || rollout > 100 {
		s.renderFlagFormError(w, r, input, i18n.T(ctx, "flags.invalid"))
		return
	}
	if _, err := s.q.GetFeatureFlag(ctx, input.Key); err == nil {
		s.renderFlagFormError(w, r, input, i18n.T(ctx, "flags.exists"))
		return
	}
	if err := s.q.UpsertFeatureFlag(ctx, sqlc.UpsertFeatureFlagParams{
		Key: input.Key, Description: input.Description, Enabled: false, Rollout: int32(rollout),
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateFlagCache()
	audit.Log(ctx, s.q, "", user.UserID, "flag.created", map[string]any{"key": input.Key, "rollout": rollout})
	Toast(w, "success", i18n.T(ctx, "flags.created"))
	Navigate(w, r, "/admin/flags")
}

// POST /admin/flags/{key}/delete — overrides cascade (FK, migration 0008).
func (s *Server) handleAdminFlagDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	key := r.PathValue("key")
	if _, err := s.q.GetFeatureFlag(ctx, key); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.DeleteFeatureFlag(ctx, key); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateFlagCache()
	audit.Log(ctx, s.q, "", user.UserID, "flag.deleted", map[string]any{"key": key})
	Toast(w, "success", i18n.T(ctx, "flags.deleted"))
	Navigate(w, r, "/admin/flags")
}

// POST /admin/flags/{key}/overrides {org, state=on|off}
func (s *Server) handleAdminFlagOverrideSet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	key := r.PathValue("key")
	if _, err := s.q.GetFeatureFlag(ctx, key); err != nil {
		http.NotFound(w, r)
		return
	}
	orgID := r.PostFormValue("org")
	if orgID == "" || (r.PostFormValue("state") != "on" && r.PostFormValue("state") != "off") {
		http.Error(w, "org and state (on|off) are required", http.StatusUnprocessableEntity)
		return
	}
	if err := s.q.UpsertFlagOverride(ctx, sqlc.UpsertFlagOverrideParams{
		FlagKey: key, OrgID: orgID, Enabled: r.PostFormValue("state") == "on",
	}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, orgID, user.UserID, "flag.override", map[string]any{
		"key": key, "enabled": r.PostFormValue("state") == "on",
	})
	Toast(w, "success", i18n.T(ctx, "flags.override_saved"))
	Navigate(w, r, "/admin/flags/"+key)
}

// POST /admin/flags/{key}/overrides/{org}/delete
func (s *Server) handleAdminFlagOverrideDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	key, orgID := r.PathValue("key"), r.PathValue("org")
	if err := s.q.DeleteFlagOverride(ctx, sqlc.DeleteFlagOverrideParams{FlagKey: key, OrgID: orgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, orgID, user.UserID, "flag.override_removed", map[string]any{"key": key})
	Toast(w, "success", i18n.T(ctx, "flags.override_deleted"))
	Navigate(w, r, "/admin/flags/"+key)
}

// renderFlagFormError re-renders the page with 422 so the invalid create
// form and its values stay on screen (project-form convention).
func (s *Server) renderFlagFormError(w http.ResponseWriter, r *http.Request, input flagCreateInput, msg string) {
	ctx := r.Context()
	flags, err := s.q.ListFeatureFlags(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(ctx, "flags.title"), Layout: templates.LayoutAdmin},
		templates.AdminFlagsPage(templates.FlagsData{
			Flags: flags, CreateKey: input.Key, CreateDescription: input.Description, CreateErr: msg,
		}))
}
