package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5/pgtype"
)

type scheduleCreateInput struct {
	Name         string `form:"name"`
	Kind         string `form:"kind"`
	Payload      string `form:"payload"`
	OrgID        string `form:"org"`
	EverySeconds string `form:"every_seconds"`
}

func validSchedule(input scheduleCreateInput) (bool, map[string]any) {
	name := strings.TrimSpace(input.Name)
	kind := strings.TrimSpace(input.Kind)
	raw := strings.TrimSpace(input.Payload)
	every, err := strconv.Atoi(strings.TrimSpace(input.EverySeconds))
	if len(name) == 0 || len(name) > 100 || err != nil || every < 60 || every > 30*86400 {
		return false, nil
	}
	if !jobs.SchedulableKindsContains(kind) {
		return false, nil
	}
	if raw == "" {
		raw = "{}"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return false, nil
	}
	return true, payload
}

// POST /admin/schedules — create (first fire = next interval unless run-now).
func (s *Server) handleAdminScheduleCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	var input scheduleCreateInput
	if err := decodeForm(r, &input); err != nil {
		s.renderScheduleFormError(w, r, input)
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	input.OrgID = strings.TrimSpace(input.OrgID)
	ok, payload := validSchedule(input)
	if !ok {
		s.renderScheduleFormError(w, r, input)
		return
	}
	raw, _ := json.Marshal(payload)
	every, _ := strconv.Atoi(strings.TrimSpace(input.EverySeconds))
	row, err := s.q.CreateSchedule(ctx, sqlc.CreateScheduleParams{
		Name: input.Name, Kind: input.Kind, Payload: raw,
		OrgID:   pgtype.Text{String: input.OrgID, Valid: input.OrgID != ""},
		EverySeconds: int32(every),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, input.OrgID, user.UserID, "schedule.created", map[string]any{
		"id": row.ID, "kind": row.Kind, "every_seconds": every,
	})
	Toast(w, "success", i18n.T(ctx, "admin.schedules.created"))
	Navigate(w, r, "/admin/schedules")
}

// POST /admin/schedules/{id}/toggle
func (s *Server) handleAdminScheduleToggle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	row, err := s.q.GetSchedule(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.SetScheduleEnabled(ctx, sqlc.SetScheduleEnabledParams{ID: id, Enabled: !row.Enabled}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, row.OrgID.String, user.UserID, "schedule.updated", map[string]any{
		"id": id, "enabled": !row.Enabled,
	})
	Toast(w, "success", i18n.T(ctx, "admin.schedules.toggled"))
	Navigate(w, r, "/admin/schedules")
}

// POST /admin/schedules/{id}/run — fires on the next scheduler pass.
func (s *Server) handleAdminScheduleRun(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	row, err := s.q.GetSchedule(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.q.RunScheduleNow(ctx, id); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, row.OrgID.String, user.UserID, "schedule.run_now", map[string]any{"id": id, "kind": row.Kind})
	Toast(w, "success", i18n.T(ctx, "admin.schedules.run_started"))
	Navigate(w, r, "/admin/schedules")
}

// POST /admin/schedules/{id}/delete
func (s *Server) handleAdminScheduleDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if err := s.q.DeleteSchedule(ctx, id); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, "", user.UserID, "schedule.deleted", map[string]any{"id": id})
	Toast(w, "success", i18n.T(ctx, "admin.schedules.deleted"))
	Navigate(w, r, "/admin/schedules")
}

// renderScheduleFormError re-renders with 422 so the invalid create form and
// its values stay on screen (project-form convention).
func (s *Server) renderScheduleFormError(w http.ResponseWriter, r *http.Request, input scheduleCreateInput) {
	ctx := r.Context()
	rows, err := s.q.ListSchedules(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgs, err := s.q.ListOrgs(r.Context())
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.schedules.title"), Layout: templates.LayoutAdmin},
		templates.AdminSchedulesPage(templates.SchedulesData{
			Items: rows, Orgs: orgs, Kinds: jobs.SchedulableKinds, Now: s.cfg.Now,
			CreateName: input.Name, CreateKind: input.Kind,
			CreatePayload: input.Payload, CreateEvery: input.EverySeconds, CreateOrg: input.OrgID,
			Invalid: true,
		}))
}
