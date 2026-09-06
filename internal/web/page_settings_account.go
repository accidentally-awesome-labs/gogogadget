package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /app/settings/account
//
// The "manage your account" link is the selected identity adapter's own
// account page, asked for through the port. It used to be built here from
// CLERK_PORTAL_URL plus a hardcoded `/user`, which meant the page knew one
// provider's URL layout and rendered no link at all under any other.
//
// An adapter that manages no profile — the dev adapter derives it from the
// subject — refuses, and the page renders its explanatory copy with no link
// rather than a 503: the rest of this page is the visitor's own settings and
// is not the provider's to withhold.
func (s *Server) handleSettingsAccount(w http.ResponseWriter, r *http.Request) {
	user := identity.UserFrom(r.Context())
	accountURL, err := s.navigator.AccountURL(s.cfg.AppURL + r.URL.Path)
	if err != nil {
		s.log.Info("identity adapter publishes no account page",
			"env", s.cfg.Env, "path", r.URL.Path, "error", err)
	}
	s.Render(w, r, Page{Title: "Account settings", Layout: templates.LayoutApp},
		templates.SettingsAccount(*user, accountURL, ""))
}
