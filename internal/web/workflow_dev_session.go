package web

import (
	"errors"
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

// devSessionCapability names the optional seam interface every dev session is
// minted through. It is the one string both refusal paths below report, so a
// caller that cannot get a session is told which capability the selected
// adapter is missing rather than being left to infer it.
const devSessionCapability = "identity.SyntheticSessionMinter"

// errNoDevSessionMinter reports that the identity adapter selected for this
// environment implements no synthetic-session capability. It is distinct from
// a mint REFUSAL — an unusable subject, say — because the two are different
// answers to a caller: one is "this deployment cannot do that at all", the
// other is "not for those arguments".
var errNoDevSessionMinter = errors.New("the identity adapter selected for this environment does not implement " + devSessionCapability)

// mintDevSession mints one synthetic session token through the selected
// adapter. It is the only path to a dev session in this package, so the token
// grammar is never spelled here.
func (s *Server) mintDevSession(userID, orgID, role string) (string, error) {
	minter := s.devSessionMinter()
	if minter == nil {
		return "", errNoDevSessionMinter
	}
	return minter.MintSession(userID, orgID, role)
}

// devSessionUnavailable renders the named failure. The point of this path is
// that it is loud: with DEV_AUTH_BYPASS on and a hosted identity adapter
// selected, the dev surface used to hand out a cookie the selected verifier
// rejects, so every guarded page bounced back to /login with no diagnostic
// anywhere. It now says which capability is missing.
func (s *Server) devSessionUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("dev session unavailable",
		"error", err, "capability", devSessionCapability,
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

// GET /dev/session?user=&org=&role= — mint a synthetic session for one
// persona triple and answer with nothing but the cookie. This is the e2e
// harness's only route to an authenticated context: the token's grammar
// belongs to whichever identity adapter the environment selected and is
// written in Go alone, so no TypeScript can build one.
//
// SECURITY BOUNDARY, stated plainly because this route hands a fully
// authenticated session for ANY user, org and role to any caller that can
// reach it. Reaching it requires DEV_AUTH_BYPASS=true — a key owned by
// ggg/system/identity-dev, whose declaration makes it a BOOT REFUSAL under
// APP_ENV=production — and the route is registered only through
// `Enabled: devAuthBypass`, the same gate /dev/login carries. It adds no
// configuration key and no lifetime of its own.
//
// It is strictly NARROWER than what it replaces. Until now the harness minted
// any triple it liked with no server involvement whatsoever, which is exactly
// why the grammar had to be restated in TypeScript. This is one more surface
// whose gate must never regress, and it is one fewer copy of the grammar.
func (s *Server) handleDevSession(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	token, err := s.mintDevSession(query.Get("user"), query.Get("org"), query.Get("role"))
	switch {
	case errors.Is(err, errNoDevSessionMinter):
		// The refusal the harness has to be able to read. A derivative that
		// selects a hosted adapter for its test environment gets this named
		// capability instead of a cookie nothing verifies and a bounce to
		// /login. Plain text, because the caller is a test harness rather
		// than a browser: the two browser routes render the page instead.
		s.log.Error("dev session unavailable",
			"error", err, "capability", devSessionCapability,
			"env", s.cfg.Env, "path", r.URL.Path)
		http.Error(w, "dev session unavailable: "+err.Error(), http.StatusServiceUnavailable)
	case err != nil:
		// A subject the adapter will not mint — one containing the grammar's
		// own separator, say. Caller error, so it is a 400 and not the 503
		// above: conflating them would name the wrong cause.
		s.log.Warn("dev session refused", "error", err, "env", s.cfg.Env)
		http.Error(w, "dev session refused: "+err.Error(), http.StatusBadRequest)
	default:
		writeDevSessionCookie(w, token)
		w.WriteHeader(http.StatusNoContent)
	}
}

// setDevSessionCookie mints through the selected adapter and reports whether
// it wrote anything. A false return has already written the response.
func (s *Server) setDevSessionCookie(w http.ResponseWriter, r *http.Request, userID, orgID, role string) bool {
	token, err := s.mintDevSession(userID, orgID, role)
	if err != nil {
		s.devSessionUnavailable(w, r, err)
		return false
	}
	writeDevSessionCookie(w, token)
	return true
}

// writeDevSessionCookie sets the session cookie every dev route issues. The
// token is opaque here: this function knows the cookie's name and flags, and
// the selected adapter knows its contents.
func writeDevSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
}
