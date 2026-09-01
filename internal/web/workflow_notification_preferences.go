package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/notify"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

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
			UserID: user.UserID, Kind: kind, InApp: r.PostForm.Has("kind_" + kind),
		})
		if err != nil {
			s.renderError(w, r, err.Error())
			return
		}
	}
	// Digest cadence rides the same form. An unknown value is ignored rather
	// than written: the column has a CHECK constraint, and a hand-crafted
	// POST should not 500 on it.
	if f := r.PostFormValue("digest_frequency"); templates.IsDigestFrequency(f) {
		if err := s.q.SetUserDigestFrequency(ctx, sqlc.SetUserDigestFrequencyParams{
			UserID: user.UserID, DigestFrequency: f,
		}); err != nil {
			s.renderError(w, r, err.Error())
			return
		}
	}
	Toast(w, "success", i18n.T(ctx, "settings.notifications_saved"))
	Navigate(w, r, "/app/settings/notifications")
}
