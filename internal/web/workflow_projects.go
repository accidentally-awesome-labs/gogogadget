package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gogogadget/gogogadget/internal/api"
	"github.com/gogogadget/gogogadget/internal/audit"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"github.com/gogogadget/gogogadget/internal/webhooks"
	"github.com/jackc/pgx/v5"
)

type projectFormInput struct {
	Name string `form:"name"`
}

// renderProjectFormError re-renders the form with 422: the fragment for
// htmx (swapped into #project-form), the full page otherwise.
func (s *Server) renderProjectFormError(w http.ResponseWriter, r *http.Request, d templates.ProjectFormData, edit bool) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	page := Page{Title: "Project", Layout: templates.LayoutApp}
	if wantsFragment(r) {
		s.Render(w, r, page, templates.ProjectForm(d))
		return
	}
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
		count, err := s.q.CountProjectsByOrg(ctx, org.OrgID)
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

	project, err := s.q.CreateProject(ctx, sqlc.CreateProjectParams{OrgID: org.OrgID, Name: name})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "project.created", map[string]any{"id": project.ID, "name": project.Name})
	webhooks.Emit(ctx, s.q, org.OrgID, "project.created", map[string]any{"id": project.ID, "name": project.Name, "status": project.Status, "org_id": org.OrgID})
	s.analytics.Capture(user.UserID, "project_created", map[string]any{"org_id": org.OrgID, "project_id": project.ID})
	Toast(w, "success", "Project created")
	Navigate(w, r, "/app/projects")
}

// projectForOrg loads the project or 404s — cross-org ids get 404, never 403
// (no existence leak).
func (s *Server) projectForOrg(w http.ResponseWriter, r *http.Request, orgID string) (sqlc.Project, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.handleNotFound(w, r)
		return sqlc.Project{}, false
	}
	project, err := s.q.GetProjectByID(r.Context(), sqlc.GetProjectByIDParams{ID: id, OrgID: orgID})
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

// POST /app/projects/{id}
func (s *Server) handleProjectUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.OrgID)
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

	if _, err := s.q.UpdateProject(ctx, sqlc.UpdateProjectParams{ID: project.ID, OrgID: org.OrgID, Name: name}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "project.updated", map[string]any{"id": project.ID, "name": name})
	webhooks.Emit(ctx, s.q, org.OrgID, "project.updated", map[string]any{"id": project.ID, "name": name, "status": project.Status, "org_id": org.OrgID})
	Toast(w, "success", "Project updated")
	Navigate(w, r, "/app/projects")
}

// POST /app/projects/{id}/archive
func (s *Server) handleProjectArchive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.OrgID)
	if !ok {
		return
	}
	if err := s.q.ArchiveProject(ctx, sqlc.ArchiveProjectParams{ID: project.ID, OrgID: org.OrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "project.updated", map[string]any{"id": project.ID, "status": "archived"})
	webhooks.Emit(ctx, s.q, org.OrgID, "project.archived", map[string]any{"id": project.ID, "name": project.Name, "status": "archived", "org_id": org.OrgID})
	Toast(w, "success", "Project archived")
	Navigate(w, r, "/app/projects")
}

// DELETE /app/projects/{id} — row swap: 200 empty, htmx removes the tr.
func (s *Server) handleProjectDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org := identity.OrgFrom(r.Context())
	user := identity.UserFrom(r.Context())
	project, ok := s.projectForOrg(w, r, org.OrgID)
	if !ok {
		return
	}
	if err := s.q.DeleteProject(ctx, sqlc.DeleteProjectParams{ID: project.ID, OrgID: org.OrgID}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	audit.Log(ctx, s.q, org.OrgID, user.UserID, "project.deleted", map[string]any{"id": project.ID, "name": project.Name})
	webhooks.Emit(ctx, s.q, org.OrgID, "project.deleted", map[string]any{"id": project.ID, "name": project.Name, "status": project.Status, "org_id": org.OrgID})
	Toast(w, "success", "Project deleted")
	w.WriteHeader(http.StatusOK)
}
