package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// GET /login → Clerk hosted sign-in (or the dev login in bypass mode).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	if s.cfg.DevAuthBypass && !s.cfg.ClerkConfigured() {
		http.Redirect(w, r, "/dev/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.cfg.ClerkPortalURL+"/sign-in?redirect_url="+s.cfg.AppURL+"/?after-auth=1", http.StatusSeeOther)
}

// GET /signup → Clerk hosted sign-up.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.authEnabled() {
		w.WriteHeader(http.StatusServiceUnavailable)
		s.Render(w, r, Page{Title: "Auth", Layout: templates.LayoutPublic}, templates.NotConfigured("Auth", "authentication"))
		return
	}
	if s.cfg.DevAuthBypass && !s.cfg.ClerkConfigured() {
		http.Redirect(w, r, "/dev/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.cfg.ClerkPortalURL+"/sign-up?redirect_url="+s.cfg.AppURL+"/?after-auth=1", http.StatusSeeOther)
}

// GET /logout → Clerk hosted sign-out (dev: clear the cookie).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DevAuthBypass && !s.cfg.ClerkConfigured() {
		s.clearSessionCookie(w)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.cfg.ClerkPortalURL+"/sign-out?redirect_url="+s.cfg.AppURL+"/", http.StatusSeeOther)
}

// GET /dev/login — zero-account mode only: set the synthetic session cookie
// for the seeded demo user and land in /app. Never registered in production.
func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	s.setDevSessionCookie(w, "user_demo", "org_demo", "org:admin")
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

// GET /dev/switch-org?org=X — dev-mode SelectOrg: rewrite the synthetic
// cookie with the chosen org (role from the membership mirror) and continue.
func (s *Server) handleDevSwitchOrg(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org")
	claims := identity.ClaimsFrom(r.Context())
	if orgID == "" || claims == nil {
		http.Redirect(w, r, "/app", http.StatusSeeOther)
		return
	}
	role := "org:member"
	if m, err := s.q.GetMembership(r.Context(), sqlc.GetMembershipParams{ClerkOrgID: orgID, ClerkUserID: claims.UserID}); err == nil {
		role = m.Role
	}
	s.setDevSessionCookie(w, claims.UserID, orgID, role)
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func (s *Server) setDevSessionCookie(w http.ResponseWriter, userID, orgID, role string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "e2e:" + userID + ":" + orgID + ":" + role,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
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

// GET /app/settings/org
func (s *Server) handleSettingsOrg(w http.ResponseWriter, r *http.Request) {
	org := identity.OrgFrom(r.Context())
	members, err := s.q.ListMembersByOrg(r.Context(), org.ClerkOrgID)
	if err != nil {
		s.renderError(w, r, err.Error())
		return
	}
	organizationURL := clerkAccountPortalLink(
		s.cfg.ClerkPortalURL,
		"/organization",
		s.cfg.AppURL+r.URL.Path,
	)
	s.Render(w, r, Page{Title: "Organization settings", Layout: templates.LayoutApp},
		templates.SettingsOrg(*org, members, organizationURL, isOrgAdmin(r)))
}
