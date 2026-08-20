package web

import (
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// POST /set-locale — persists the locale preference (cookie) and hard-redirects
// back. Hard redirect on purpose: the locale changes the SHELL (nav, footer,
// sidebar), which boosted navigation never re-renders. Public route on the
// main mux, so the standard chain (incl. CSRF) applies — the switcher forms
// use hx-post and inherit the body's X-CSRF-Token header.
func (s *Server) handleSetLocale(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	lang := r.PostFormValue("lang")
	returnTo := safeReturnTo(r.PostFormValue("returnTo"))

	// An empty lang clears the choice instead of pinning one: the user is
	// asking to follow their browser again, and storing "en" would override a
	// Spanish Accept-Language forever.
	if lang == "" {
		http.SetCookie(w, &http.Cookie{
			Name: i18n.CookieName, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: s.cfg.Production(), SameSite: http.SameSiteLaxMode,
		})
		s.storeLocale(r, "")
		Redirect(w, r, returnTo)
		return
	}

	tag, ok := i18n.ParseSupported(lang)
	if !ok {
		http.Error(w, "unsupported locale", http.StatusUnprocessableEntity)
		return
	}

	s.setPrefCookie(w, i18n.CookieName, tag.String())
	s.storeLocale(r, tag.String())
	Redirect(w, r, returnTo)
}

// storeLocale persists the choice for a signed-in user. The cookie only
// carries it to the next request on THIS browser; the row is what makes it
// follow them to another device — and what the digest email reads.
func (s *Server) storeLocale(r *http.Request, locale string) {
	user := identity.UserFrom(r.Context())
	if user == nil {
		return
	}
	if err := s.q.SetUserLocale(r.Context(), sqlc.SetUserLocaleParams{
		ClerkUserID: user.ClerkUserID, Locale: locale,
	}); err != nil {
		s.log.Error("set locale", "error", err, "user", user.ClerkUserID)
	}
}

// safeReturnTo accepts only same-site paths: must start with "/" and must not
// start with "//" (protocol-relative → off-origin).
func safeReturnTo(s string) string {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return s
	}
	return "/"
}
