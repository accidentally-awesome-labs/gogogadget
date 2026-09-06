package web

import (
	"net/http"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /login → the selected identity adapter's sign-in page.
//
// There is no DEV_AUTH_BYPASS branch here any more. That branch spelled a
// specific adapter's route (`/dev/login`) in the neutral package and read that
// adapter's key to decide when to use it; the dev adapter now answers with its
// own route, and refuses when its bypass is off — which is exactly when that
// route is not registered.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" {
		returnTo = s.cfg.AppURL + "/?after-auth=1"
	}
	target, err := s.navigator.LoginURL(returnTo)
	if err != nil {
		s.providerDestinationUnavailable(w, r, "identity", "sign-in", err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// GET /signup → the selected identity adapter's sign-up page.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	target, err := s.navigator.SignupURL(r.URL.Query().Get("return_to"))
	if err != nil {
		s.providerDestinationUnavailable(w, r, "identity", "sign-up", err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// GET /logout → clear this application's session cookie, then hand off to the
// selected adapter's sign-out page.
//
// The cookie is cleared for every adapter, not only under the dev bypass:
// `__session` is the cookie THIS application reads on every request, so
// redirecting to a hosted sign-out while leaving it in place left the visitor
// signed in here until the provider's script happened to clear it.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w)
	target, err := s.navigator.LogoutURL(s.cfg.AppURL + "/")
	if err != nil {
		s.providerDestinationUnavailable(w, r, "identity", "sign-out", err)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
	})
}
