package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/projects/new
func (s *Server) handleProjectNew(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, Page{Title: "New project", Layout: templates.LayoutApp},
		templates.ProjectNewPage(templates.ProjectFormData{Plan: identity.PlanFrom(r.Context())}))
}
