package web

import (
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// devAuthBypass gates every zero-account dev route. config refuses
// DEV_AUTH_BYPASS under APP_ENV=production, so this is false on a live site.
func (s *Server) devAuthBypass() bool { return s.cfg.DevAuthBypass }

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
	if m, err := s.q.GetMembership(r.Context(), sqlc.GetMembershipParams{OrgID: orgID, UserID: claims.UserID}); err == nil {
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
