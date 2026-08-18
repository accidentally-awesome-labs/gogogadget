package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/notifications — per-kind in-app mutes, default-on.
func (s *Server) handleSettingsNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	rows, err := s.q.ListNotificationPreferencesByUser(ctx, user.ClerkUserID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	prefs := map[string]bool{}
	for _, k := range notify.Kinds {
		prefs[k] = true // absent row = default-on
	}
	for _, row := range rows {
		prefs[row.Kind] = row.InApp
	}
	s.Render(w, r, Page{Title: i18n.T(ctx, "settings.notifications_title"), Layout: templates.LayoutApp},
		templates.SettingsNotificationsPage(prefs))
}

// POST /app/settings/notifications — checkbox presence per kind: an
// unchecked box is simply absent from the form body.
func (s *Server) handleSettingsNotificationsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	for _, kind := range notify.Kinds {
		err := s.q.UpsertNotificationPreference(ctx, sqlc.UpsertNotificationPreferenceParams{
			ClerkUserID: user.ClerkUserID, Kind: kind, InApp: r.PostForm.Has("kind_" + kind),
		})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
	}
	Toast(w, "success", i18n.T(ctx, "settings.notifications_saved"))
	Navigate(w, r, "/app/settings/notifications")
}
