package web

import (
	"net/http"
	"net/url"

	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /login → Clerk hosted sign-in (or the dev login in bypass mode).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	if s.cfg.BoolValue("DEV_AUTH_BYPASS") {
		http.Redirect(w, r, "/dev/login", http.StatusSeeOther)
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" {
		returnTo = s.cfg.AppURL + "/?after-auth=1"
	}
	http.Redirect(w, r, s.navigator.LoginURL(returnTo), http.StatusSeeOther)
}

// GET /signup → Clerk hosted sign-up.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	if s.cfg.BoolValue("DEV_AUTH_BYPASS") {
		http.Redirect(w, r, "/dev/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.navigator.SignupURL(r.URL.Query().Get("return_to")), http.StatusSeeOther)
}

// GET /logout → Clerk hosted sign-out (dev: clear the cookie).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.BoolValue("DEV_AUTH_BYPASS") {
		s.clearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.navigator.AccountURL(), http.StatusSeeOther)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1,
	})
}

// accountPortalLink builds a hosted account-portal URL from the base the
// selected identity adapter declares. Named for what it does rather than for
// who currently provides it: the base arrives as a by-key configuration read,
// so an adapter that publishes no portal yields the empty string and the
// caller renders no link.
//
// It duplicates identity.Navigator.AccountURL's job from the wrong side of the
// seam — the port exists and the adapter implements it. Recorded in
// task-t-report.md as the next coupling rather than re-plumbed here.
func accountPortalLink(baseURL, page, redirectURL string) string {
	if baseURL == "" {
		return ""
	}
	return baseURL + page + "?redirect_url=" + url.QueryEscape(redirectURL)
}
