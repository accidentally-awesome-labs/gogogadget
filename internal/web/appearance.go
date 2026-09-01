package web

import (
	"context"
	"net/http"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/gogogadget/gogogadget/internal/identity"
)

// Appearance preferences: locale and theme.
//
// Both have two homes on purpose. The cookie is the *transport* — it works
// for signed-out visitors, and it is readable before any database call, which
// is what lets the pre-paint theme script and i18n.Detect run early in the
// chain. The user row is the *truth* for anyone signed in, so a preference
// set on a laptop is already applied on the first request from a phone.
//
// Locale precedence, in order:
//
//  1. ?lang= — a deliberate per-request override, never persisted
//  2. the user row — set from the switcher; survives devices
//  3. the cookie — how signed-out visitors keep a choice
//  4. Accept-Language, then English
//
// Steps 1, 3 and 4 are resolved by i18n.Detect (before the session is known);
// step 2 is applied here, because the user row only exists after the session
// loads. That ordering is why this runs inside sessionLoad rather than in the
// i18n middleware — see /docs/i18n.

const themeCookieName = "theme"

// Themes are the values users may store. "system" defers to the OS setting,
// which only the browser can answer, so the server renders no class for it.
var Themes = []string{"system", "light", "dark"}

func isTheme(v string) bool {
	for _, t := range Themes {
		if t == v {
			return true
		}
	}
	return false
}

// applyStoredAppearance upgrades the request with the signed-in user's saved
// preferences and re-syncs the cookies when they disagree, so the next
// request (and the pre-paint script, which cannot query the database) sees
// the same answer.
func (s *Server) applyStoredAppearance(w http.ResponseWriter, r *http.Request, ctx context.Context, user sqlc.User) context.Context {
	// Re-sync cookies on safe requests only. A POST to /set-locale or
	// /set-theme is precisely the request that is CHANGING the preference:
	// syncing here would emit a second Set-Cookie for the same name, racing
	// the handler's own, and leave the answer to header ordering.
	sync := r.Method == http.MethodGet || r.Method == http.MethodHead

	if user.Locale != "" && r.URL.Query().Get("lang") == "" {
		tag := i18n.ParseOrDefault(user.Locale)
		ctx = i18n.WithTag(ctx, tag)
		if c, err := r.Cookie(i18n.CookieName); sync && (err != nil || c.Value != tag.String()) {
			s.setPrefCookie(w, i18n.CookieName, tag.String())
		}
	}
	if isTheme(user.Theme) {
		if c, err := r.Cookie(themeCookieName); sync && (err != nil || c.Value != user.Theme) {
			s.setPrefCookie(w, themeCookieName, user.Theme)
		}
	}
	return ctx
}

// setPrefCookie writes a year-long preference cookie. Not HttpOnly for the
// theme: the pre-paint script in app.js reads it to avoid a flash of the
// wrong theme, which is the entire reason the cookie exists.
func (s *Server) setPrefCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: name != themeCookieName,
		Secure:   s.cfg.Production(),
		SameSite: http.SameSiteLaxMode,
	})
}

// resolveTheme answers "what should the server render right now": the stored
// preference for a signed-in user, else the cookie, else "system".
func resolveTheme(r *http.Request, user *sqlc.User) string {
	if user != nil && isTheme(user.Theme) && user.Theme != "system" {
		return user.Theme
	}
	if c, err := r.Cookie(themeCookieName); err == nil && isTheme(c.Value) {
		return c.Value
	}
	return "system"
}

// POST /set-theme — persists the theme (cookie + user row when signed in).
//
// The caller always names the value it wants; the server never infers a flip.
// It cannot: with the preference set to "system" only the browser knows which
// way the OS points, so a server-side flip would disagree with the class the
// user is looking at. The topbar toggle therefore posts the value it just
// applied (see themeToggle in app.js) and the settings buttons post theirs.
func (s *Server) handleSetTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	next := r.PostFormValue("theme")
	if !isTheme(next) {
		http.Error(w, "unsupported theme", http.StatusUnprocessableEntity)
		return
	}
	s.setPrefCookie(w, themeCookieName, next)
	if user := identity.UserFrom(r.Context()); user != nil {
		if err := s.q.SetUserTheme(r.Context(), sqlc.SetUserThemeParams{
			UserID: user.UserID, Theme: next,
		}); err != nil {
			s.log.Error("set theme", "error", err, "user", user.UserID)
		}
	}
	// A form that names where it came from gets a HARD redirect: the theme
	// class lives on <html>, which boosted navigation never re-renders. The
	// topbar toggle sends no returnTo — it already flipped the class itself,
	// so there is nothing to swap and nothing to reload.
	if returnTo := r.PostFormValue("returnTo"); returnTo != "" {
		Redirect(w, r, safeReturnTo(returnTo))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
