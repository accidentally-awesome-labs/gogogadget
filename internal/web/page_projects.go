package web

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

const projectsPageSize = 20

// GET /app/projects — search + pagination.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	total, err := s.q.CountProjectsByOrgSearch(ctx, sqlc.CountProjectsByOrgSearchParams{OrgID: org.OrgID, Column2: q})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/projectsPageSize)), 1)
	projects, err := s.q.ListProjectsByOrg(ctx, sqlc.ListProjectsByOrgParams{
		OrgID: org.OrgID, Column2: q,
		Limit: projectsPageSize, Offset: int32((page - 1) * projectsPageSize),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	count, err := s.q.CountProjectsByOrg(ctx, org.OrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	d := templates.ProjectListData{
		Projects: projects, Query: q, Page: page, TotalPages: totalPages,
		Plan: identity.PlanFrom(ctx), Count: count,
	}
	pageData := Page{Title: "Projects", Layout: templates.LayoutApp}
	if wantsFragment(r) {
		s.Render(w, r, pageData, templates.ProjectsTable(d))
		return
	}
	s.Render(w, r, pageData, templates.ProjectsPage(d))
}
