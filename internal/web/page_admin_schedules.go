package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/jobs"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/schedules — recurring-work table + create form.
func (s *Server) handleAdminSchedules(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.q.ListSchedules(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	orgs, err := s.q.ListOrgs(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.schedules.title"), Layout: templates.LayoutAdmin},
		templates.AdminSchedulesPage(templates.SchedulesData{Items: rows, Orgs: orgs, Kinds: jobs.SchedulableKinds, Now: s.cfg.Now}))
}
