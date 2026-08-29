package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

type announcementFormInput struct {
	Kind    string `form:"kind"`
	Message string `form:"message"`
	Url     string `form:"url"`
}

var validAnnouncementKinds = map[string]bool{"info": true, "warning": true, "critical": true}

func validAnnouncement(input announcementFormInput) bool {
	kind := strings.TrimSpace(input.Kind)
	message := strings.TrimSpace(input.Message)
	url := strings.TrimSpace(input.Url)
	return validAnnouncementKinds[kind] &&
		len(message) > 0 && len(message) <= 280 &&
		(url == "" || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://"))
}

// POST /admin/announcements — create (inactive; activation is explicit).
func (s *Server) handleAdminAnnouncementCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var input announcementFormInput
	if err := decodeForm(r, &input); err != nil || !validAnnouncement(input) {
		s.renderAnnouncementFormError(w, r, input)
		return
	}
	_, err := s.q.CreateAnnouncement(ctx, sqlc.CreateAnnouncementParams{
		Kind:    strings.TrimSpace(input.Kind),
		Message: strings.TrimSpace(input.Message),
		Url:     strings.TrimSpace(input.Url),
	})
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateAnnouncementCache()
	Toast(w, "success", i18n.T(ctx, "admin.announcements.created"))
	Navigate(w, r, "/admin/announcements")
}

// POST /admin/announcements/{id}/activate — one-active: deactivate the rest
// first; the partial unique index enforces the invariant at the DB level.
func (s *Server) handleAdminAnnouncementActivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseAnnouncementID(w, r)
	if !ok {
		return
	}
	if err := s.q.DeactivateAnnouncements(ctx); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	if err := s.q.SetAnnouncementActive(ctx, sqlc.SetAnnouncementActiveParams{ID: id, Active: true}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateAnnouncementCache()
	Toast(w, "success", i18n.T(ctx, "admin.announcements.activated"))
	Navigate(w, r, "/admin/announcements")
}

// POST /admin/announcements/{id}/deactivate
func (s *Server) handleAdminAnnouncementDeactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseAnnouncementID(w, r)
	if !ok {
		return
	}
	if err := s.q.SetAnnouncementActive(ctx, sqlc.SetAnnouncementActiveParams{ID: id, Active: false}); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateAnnouncementCache()
	Toast(w, "success", i18n.T(ctx, "admin.announcements.deactivated"))
	Navigate(w, r, "/admin/announcements")
}

// POST /admin/announcements/{id}/delete
func (s *Server) handleAdminAnnouncementDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := parseAnnouncementID(w, r)
	if !ok {
		return
	}
	if err := s.q.DeleteAnnouncement(ctx, id); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	s.invalidateAnnouncementCache()
	Toast(w, "success", i18n.T(ctx, "admin.announcements.deleted"))
	Navigate(w, r, "/admin/announcements")
}

func parseAnnouncementID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// renderAnnouncementFormError re-renders with 422 so the invalid form and its
// values stay on screen (project-form convention).
func (s *Server) renderAnnouncementFormError(w http.ResponseWriter, r *http.Request, input announcementFormInput) {
	ctx := r.Context()
	w.WriteHeader(http.StatusUnprocessableEntity)
	s.Render(w, r, Page{Title: i18n.T(ctx, "admin.announcements.title"), Layout: templates.LayoutAdmin},
		templates.AnnouncementForm(templates.AnnouncementsData{
			Kind: input.Kind, Message: input.Message, Url: input.Url, Invalid: true,
		}))
}
