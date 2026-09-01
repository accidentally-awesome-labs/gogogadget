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
	if s.cfg.DevAuthBypass {
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
	if s.cfg.DevAuthBypass {
		http.Redirect(w, r, "/dev/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.navigator.SignupURL(r.URL.Query().Get("return_to")), http.StatusSeeOther)
}

// GET /logout → Clerk hosted sign-out (dev: clear the cookie).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DevAuthBypass {
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

func clerkAccountPortalLink(baseURL, page, redirectURL string) string {
	if baseURL == "" {
		return ""
	}
	return baseURL + page + "?redirect_url=" + url.QueryEscape(redirectURL)
}
