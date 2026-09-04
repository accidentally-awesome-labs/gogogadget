package web

import (
	"net/http"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/identity"
	"github.com/gogogadget/gogogadget/internal/web/templates"
)

// devAuthBypass gates every zero-account dev route. The key belongs to
// ggg/system/identity-dev, whose declaration also carries the boot refusal
// under APP_ENV=production, so this is false on a live site. It is read by key
// rather than by field because this module does not declare it: deselecting
// the dev adapter must take the field with it, and leave this reading false.
func (s *Server) devAuthBypass() bool { return s.cfg.BoolValue("DEV_AUTH_BYPASS") }

// devSessionMinter is the selected identity adapter's synthetic-session
// capability, or nil when the adapter selected for this environment does not
// offer one.
//
// This is the whole of this module's dependency on ggg/system/identity-dev,
// and it is deliberately a per-environment type assertion rather than a
// manifest `requires`: an adapter is chosen per environment, so requiring one
// would pin it into every install and make deselecting it refuse. What used to
// hold the two together was a hardcoded "e2e:" literal here — a provider's
// token shape written into a neutral workflow, which compiled happily against
// any adapter and produced a cookie nothing could verify.
func (s *Server) devSessionMinter() identity.SyntheticSessionMinter {
	minter, _ := s.verifier.(identity.SyntheticSessionMinter)
	return minter
}

// devSessionUnavailable renders the named failure. The point of this path is
// that it is loud: with DEV_AUTH_BYPASS on and a hosted identity adapter
// selected, the dev surface used to hand out a cookie the selected verifier
// rejects, so every guarded page bounced back to /login with no diagnostic
// anywhere. It now says which capability is missing.
func (s *Server) devSessionUnavailable(w http.ResponseWriter, r *http.Request) {
	s.log.Error("dev session unavailable",
		"reason", "the identity adapter selected for this environment does not implement identity.SyntheticSessionMinter",
		"env", s.cfg.Env, "path", r.URL.Path)
	w.WriteHeader(http.StatusServiceUnavailable)
	s.Render(w, r, Page{Title: "Dev session", Layout: templates.LayoutPublic},
		templates.NotConfigured("Dev session", "a zero-account identity adapter"))
}

// GET /dev/login — zero-account mode only: set the synthetic session cookie
// for the seeded demo user and land in /app. Never registered in production.
func (s *Server) handleDevLogin(w http.ResponseWriter, r *http.Request) {
	if !s.setDevSessionCookie(w, r, "user_demo", "org_demo", "org:admin") {
		return
	}
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
	userSubject := claims.UserID
	orgSubject := orgID
	if mapped, err := s.q.GetIdentitySubjectByUser(r.Context(), claims.UserID); err == nil {
		userSubject = mapped.Subject
	}
	if mapped, err := s.q.GetIdentityOrganizationByOrg(r.Context(), orgID); err == nil {
		orgSubject = mapped.Subject
	}
	if !s.setDevSessionCookie(w, r, userSubject, orgSubject, role) {
		return
	}
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

// setDevSessionCookie mints through the selected adapter and reports whether
// it wrote anything. A false return has already written the response.
func (s *Server) setDevSessionCookie(w http.ResponseWriter, r *http.Request, userID, orgID, role string) bool {
	minter := s.devSessionMinter()
	if minter == nil {
		s.devSessionUnavailable(w, r)
		return false
	}
	token, err := minter.MintSession(userID, orgID, role)
	if err != nil {
		s.log.Error("mint dev session", "error", err)
		s.devSessionUnavailable(w, r)
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
	return true
}
