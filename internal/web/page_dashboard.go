package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app — dashboard: stat cards, recent activity, getting-started checklist.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())

	projects, err := s.q.CountProjectsByOrg(ctx, org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	members, err := s.q.CountMembersByOrg(ctx, org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	recent, err := s.q.RecentAuditByOrg(ctx, sqlc.RecentAuditByOrgParams{OrgID: sqlcText(org.OrgID), Limit: 10})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: "Dashboard", Layout: templates.LayoutApp}, templates.Dashboard(templates.DashboardData{
		ActiveProjects: projects,
		MemberCount:    members,
		Plan:           identity.PlanFrom(ctx),
		Recent:         recent,
		Now:            s.cfg.Now(),
	}))
}
