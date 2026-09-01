package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/notifications — per-kind in-app mutes, default-on.
func (s *Server) handleSettingsNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := identity.UserFrom(r.Context())
	rows, err := s.q.ListNotificationPreferencesByUser(ctx, user.UserID)
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
		templates.SettingsNotificationsPage(prefs, user.DigestFrequency))
}
