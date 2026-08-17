package web

import (
	"net/http"
	"strings"

	"github.com/gogogadget/gogogadget/internal/i18n"
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
	tag, ok := i18n.ParseSupported(lang)
	if !ok {
		http.Error(w, "unsupported locale", http.StatusUnprocessableEntity)
		return
	}
	returnTo := safeReturnTo(r.PostFormValue("returnTo"))

	http.SetCookie(w, &http.Cookie{
		Name:     i18n.CookieName,
		Value:    tag.String(),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60, // 1 year
		HttpOnly: true,
		Secure:   s.cfg.Production(),
		SameSite: http.SameSiteLaxMode,
	})
	Redirect(w, r, returnTo)
}

// safeReturnTo accepts only same-site paths: must start with "/" and must not
// start with "//" (protocol-relative → off-origin).
func safeReturnTo(s string) string {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return s
	}
	return "/"
}
