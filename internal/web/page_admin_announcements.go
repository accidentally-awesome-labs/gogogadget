package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /admin/announcements — list + create form.
func (s *Server) handleAdminAnnouncements(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	items, err := s.q.ListAnnouncements(ctx)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.announcements.title"), Layout: templates.LayoutAdmin},
		templates.AdminAnnouncementsPage(templates.AnnouncementsData{Items: items}))
}
