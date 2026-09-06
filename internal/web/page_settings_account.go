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
// session subject — refuses, and this page degrades rather than 503ing: the
// rest of it is the visitor's own settings and is not the provider's to
// withhold. The refusal is deliberately dropped rather than logged. It is not
// an event: it is the same answer on every view for a whole environment, and
// the page states it on screen, which is where a visitor can act on it.
// providerDestinationUnavailable logs the case where the refusal actually
// costs someone the request.
func (s *Server) handleSettingsAccount(w http.ResponseWriter, r *http.Request) {
	user := identity.UserFrom(r.Context())
	// Empty on refusal, which is the template's signal to render the local
	// caption instead of the provider one. One value, one fact.
	accountURL, _ := s.navigator.AccountURL(s.cfg.AppURL + r.URL.Path)
	s.Render(w, r, Page{Title: "Account settings", Layout: templates.LayoutApp},
		templates.SettingsAccount(*user, accountURL, ""))
}
