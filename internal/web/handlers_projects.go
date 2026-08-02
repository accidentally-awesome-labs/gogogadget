package web

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"

	form "github.com/go-playground/form/v4"
	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/jackc/pgx/v5"
)

const projectsPageSize = 20

var formDecoder = form.NewDecoder()

// decodeForm parses the body and decodes it into dst.
func decodeForm(r *http.Request, dst any) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	return formDecoder.Decode(dst, r.Form)
}

// GET /app — dashboard: stat cards, recent activity, getting-started checklist.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())

	projects, err := s.q.CountProjectsByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	members, err := s.q.CountMembersByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	recent, err := s.q.RecentAuditByOrg(ctx, sqlc.RecentAuditByOrgParams{ClerkOrgID: sqlcText(org.ClerkOrgID), Limit: 10})
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

// GET /app/projects — search + pagination.
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	total, err := s.q.CountProjectsByOrgSearch(ctx, sqlc.CountProjectsByOrgSearchParams{ClerkOrgID: org.ClerkOrgID, Column2: q})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	totalPages := max(int(math.Ceil(float64(total)/projectsPageSize)), 1)
	projects, err := s.q.ListProjectsByOrg(ctx, sqlc.ListProjectsByOrgParams{
		ClerkOrgID: org.ClerkOrgID, Column2: q,
		Limit: projectsPageSize, Offset: int32((page - 1) * projectsPageSize),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	count, err := s.q.CountProjectsByOrg(ctx, org.ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}

	d := templates.ProjectListData{
		Projects: projects, Query: q, Page: page, TotalPages: totalPages,
		Plan: identity.PlanFrom(ctx), Count: count,
	}
	pageData := Page{Title: "Projects", Layout: templates.LayoutApp}
	if IsHX(r) && !IsBoosted(r) {
		s.Render(w, r, pageData, templates.ProjectsTable(d))
		return
	}
	s.Render(w, r, pageData, templates.ProjectsPage(d))
}

// GET /app/projects/new
func (s *Server) handleProjectNew(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "New project", Layout: templates.LayoutApp},
		templates.ProjectNewPage(templates.ProjectFormData{Plan: identity.PlanFrom(r.Context())}))
}

type projectFormInput struct {
	Name string `form:"name"`
}

// renderProjectFormError re-renders the form fragment with 422.
func (s *Server) renderProjectFormError(w http.ResponseWriter, r *http.Request, d templates.ProjectFormData, edit bool) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	page := Page{Title: "Project", Layout: templates.LayoutApp}
	if edit {
		s.Render(w, r, page, templates.ProjectEditPage(d))
		return
	}
	s.Render(w, r, page, templates.ProjectNewPage(d))
}

// POST /app/projects
func (s *Server) handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	plan := identity.PlanFrom(ctx)

	var input projectFormInput
	if err := decodeForm(r, &input); err != nil {
		s.renderProjectFormError(w, r, templates.ProjectFormData{Plan: plan}, false)
		return
	}
	name, nameErr := api.ValidateProjectName(input.Name)
	d := templates.ProjectFormData{Name: name, NameErr: nameErr, Plan: plan}
	if nameErr != "" {
		s.renderProjectFormError(w, r, d, false)
		return
	}

	// Freemium enforcement: per-action limit, not a route lock.
	if plan.MaxProjects > 0 {
		count, err := s.q.CountProjectsByOrg(ctx, org.ClerkOrgID)
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
		if count >= int64(plan.MaxProjects) {
			d.LimitHit = true
			s.renderProjectFormError(w, r, d, false)
			return
		}
	}

	project, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{ClerkOrgID: org.ClerkOrgID, Name: name})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "project.created", map[string]any{"id": project.ID, "name": project.Name})
	Toast(w, "success", "Project created")
	HXRedirect(w, "/app/projects")
}

// projectForOrg loads the project or 404s — cross-org ids get 404, never 403
// (no existence leak).
func (s *Server) projectForOrg(w http.ResponseWriter, r *http.Request, orgID string) (sqlc.Project, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleNotFound(w, r)
		return sqlc.Project{}, false
	}
	project, err := s.q.GetProjectByID(r.Context(), sqlc.GetProjectByIDParams{ID: id, ClerkOrgID: orgID})
	if errors.Is(err, pgx.ErrNoRows) {
		s.handleNotFound(w, r)
		return sqlc.Project{}, false
	}
	if err != nil {
		s.renderError(w, r, err.Error())
		return sqlc.Project{}, false
	}
	return project, true
}

// GET /app/projects/{id}/edit
func (s *Server) handleProjectEdit(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.ClerkOrgID)
	if !ok {
		return
	}
	s.Render(w, r, Page{Title: "Edit project", Layout: templates.LayoutApp},
		templates.ProjectEditPage(templates.ProjectFormData{ID: project.ID, Name: project.Name, Plan: identity.PlanFrom(r.Context())}))
}

// POST /app/projects/{id}
func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.ClerkOrgID)
	if !ok {
		return
	}

	var input projectFormInput
	if err := decodeForm(r, &input); err != nil {
		s.renderProjectFormError(w, r, templates.ProjectFormData{ID: project.ID, Plan: identity.PlanFrom(ctx)}, true)
		return
	}
	name, nameErr := api.ValidateProjectName(input.Name)
	if nameErr != "" {
		s.renderProjectFormError(w, r, templates.ProjectFormData{ID: project.ID, Name: name, NameErr: nameErr, Plan: identity.PlanFrom(ctx)}, true)
		return
	}

	if _, err := s.q.UpdateProject(ctx, sqlc.UpdateProjectParams{ID: project.ID, ClerkOrgID: org.ClerkOrgID, Name: name}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "project.updated", map[string]any{"id": project.ID, "name": name})
	Toast(w, "success", "Project updated")
	HXRedirect(w, "/app/projects")
}

// POST /app/projects/{id}/archive
func (s *Server) handleProjectArchive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.ClerkOrgID)
	if !ok {
		return
	}
	if err := s.q.ArchiveProject(ctx, sqlc.ArchiveProjectParams{ID: project.ID, ClerkOrgID: org.ClerkOrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "project.updated", map[string]any{"id": project.ID, "status": "archived"})
	Toast(w, "success", "Project archived")
	HXRedirect(w, "/app/projects")
}

// DELETE /app/projects/{id} — row swap: 200 empty, htmx removes the tr.
func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.ClerkOrgID)
	if !ok {
		return
	}
	if err := s.q.DeleteProject(ctx, sqlc.DeleteProjectParams{ID: project.ID, ClerkOrgID: org.ClerkOrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.ClerkOrgID, user.ClerkUserID, "project.deleted", map[string]any{"id": project.ID, "name": project.Name})
	Toast(w, "success", "Project deleted")
	w.WriteHeader(http.StatusOK)
}
