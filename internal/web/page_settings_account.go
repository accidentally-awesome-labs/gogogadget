package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/account
func (s *Server) handleSettingsAccount(w http.ResponseWriter, r *http.Request) {
	user := identity.UserFrom(r.Context())
	accountURL := clerkAccountPortalLink(
		s.cfg.ClerkPortalURL,
		"/user",
		s.cfg.AppURL+r.URL.Path,
	)
	s.Render(w, r, Page{Title: "Account settings", Layout: templates.LayoutApp},
		templates.SettingsAccount(*user, accountURL, ""))
}
