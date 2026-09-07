package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
// adapter and refuses one the session cookie cannot carry. It is the only
// path to a dev session in this package, so the token grammar is never
// spelled here.
func (s *Server) mintDevSession(userID, orgID, role string) (string, error) {
	minter := s.devSessionMinter()
	if minter == nil {
		return "", errNoDevSessionMinter
	}
	token, err := minter.MintSession(userID, orgID, role)
	if err != nil {
		return "", err
	}
	if i := devSessionUnsafeByte(token); i >= 0 {
		// token[i:i+1], not string(token[i]): the latter converts a byte
		// through a rune, so 0xc3 would be reported as "Ã" — a character the
		// caller never sent — instead of as the byte the cookie drops.
		return "", fmt.Errorf("the session cookie cannot carry byte %#02x (%q): it would be dropped in transport, "+
			"answering with a session for a different subject or role than was asked for", token[i], token[i:i+1])
	}
	return token, nil
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
// It is NOT narrower than what it replaces, which is what an earlier version
// of this comment claimed and an experiment refuted. The old path was
// Playwright's context.addCookies — client-side, and so reachable only by
// script ALREADY EXECUTING on this origin. This is a GET, and a GET is exempt
// from CSRF by construction (`csrf` is nosurf and method-gated), while
// SameSite=Lax permits a cookie to be SET on a top-level navigation: a link
// on any page a developer clicked reached this handler and planted a session
// for a subject the linking site chose. That is what devSessionCrossSite
// below refuses, and it costs the harness nothing because Playwright's
// context.request is not a browser and sends no Origin, Referer or
// Sec-Fetch-* at all. What IS true of the route is narrower and duller: it is
// one fewer copy of the token grammar, and one more surface whose gate must
// never regress.
//
// What a session obtained here can do, stated because the writes are easy to
// miss from the reply — which is empty. Spending the cookie on a guarded page
// runs identity.SessionLoader, which for an unknown subject CREATES the user,
// CREATES the org and grants the requested role as a MEMBERSHIP
// (internal/identity/session/session.go). `role` cannot escalate past
// org:admin, because /admin is keyed on users.admin_role rather than on
// claims — but the SUBJECT can: a newly created user whose email matches
// ADMIN_EMAIL is promoted to platform admin on creation, and the zero-account
// fetcher derives that email as <subject>@gogogadget.dev. So the escalation
// lives in the choice of `user`, not in the choice of `role`.
func (s *Server) handleDevSession(w http.ResponseWriter, r *http.Request) {
	// Cross-site first: everything after this line hands out a session.
	if header := devSessionCrossSite(r); header != "" {
		s.log.Warn("dev session refused: cross-site request",
			"header", header, "env", s.cfg.Env, "path", r.URL.Path)
		http.Error(w, "dev session refused: cross-site request ("+header+")", http.StatusForbidden)
		return
	}
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

// devSessionCrossSite names the header proving a request did not originate on
// this application's own pages, or returns "" when nothing does.
//
// Fetch metadata is the check that actually fires: a cross-site top-level
// navigation sends `Sec-Fetch-Site: cross-site` and no `Origin` whatsoever,
// because a GET navigation carries none. `Origin` and `Referer` are checked
// too, for a caller that sends one without fetch metadata. All three absent
// is the ordinary non-browser case — the e2e harness, curl — and is allowed,
// because refusing it would refuse the only caller this route exists for
// while stopping nobody: a program that chooses its own headers is not the
// threat here. The threat is a browser acting on a developer's click with
// the developer's cookie jar, and a browser always tells the truth in these
// three headers.
func devSessionCrossSite(r *http.Request) string {
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "", "same-origin", "none":
	default:
		return "Sec-Fetch-Site: " + site
	}
	if origin := r.Header.Get("Origin"); origin != "" && !sameHostAsRequest(origin, r) {
		return "Origin: " + origin
	}
	if referer := r.Header.Get("Referer"); referer != "" && !sameHostAsRequest(referer, r) {
		return "Referer: " + referer
	}
	return ""
}

// sameHostAsRequest reports whether an absolute URL names this request's own
// host. The request's Host is the only authority available: a dev server is
// reached by whatever the developer typed — localhost:18080, or
// host.docker.internal:18080 inside the pinned visual container — so no
// configuration key can decide this. Host and port only; the scheme is left
// out because deriving this application's own scheme behind a proxy is
// guesswork, and a same-host attacker on the other scheme is not a threat a
// route that only exists under DEV_AUTH_BYPASS can address.
func sameHostAsRequest(rawURL string, r *http.Request) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

// devSessionUnsafeByte reports the index of the first byte of a token the
// session cookie cannot carry, or -1 when every byte survives.
//
// http.SetCookie does not refuse a value it cannot represent: it DROPS every
// byte outside this set, logs "dropping invalid bytes", and sends what is
// left. So `role=admin%3Bfoo` answered 204 with a session whose role was
// `adminfoo`, and `user=user_%0A` one for `user_` — the route said "here is
// your session" and handed back a session for a different subject or role
// than was asked for, which is the same validate-here/mutate-there asymmetry
// this workflow exists to remove, reproduced across the transport boundary
// instead of the Go/TypeScript one. Space and comma are absent from the
// refusal deliberately: Go quotes a value containing either and unquotes it
// symmetrically on the way back in, so those round-trip exactly.
//
// The check is over the token's bytes rather than over user, org and role
// because the token is opaque here by design — its grammar belongs to the
// selected adapter. What this package owns is the cookie, so what it can
// check is what the cookie can hold, for any adapter that ever mints one.
func devSessionUnsafeByte(token string) int {
	for i := 0; i < len(token); i++ {
		if b := token[i]; b < 0x20 || b >= 0x7f || b == '"' || b == ';' || b == '\\' {
			return i
		}
	}
	return -1
}
