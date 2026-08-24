package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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
