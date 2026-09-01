package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Appearance preferences live in two places (cookie for transport, user row
// for truth). These tests pin the precedence between them, because getting it
// wrong is invisible until someone's phone speaks the wrong language.

func TestStoredLocaleBeatsCookie(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_loc", "org_loc", "org:admin")
	require.NoError(t, s.q.SetUserLocale(t.Context(), sqlc.SetUserLocaleParams{
		UserID: "user_loc", Locale: "es",
	}))

	// A stale cookie from an earlier anonymous visit on this browser.
	req := []*http.Cookie{
		sessionCookie("user_loc", "org_loc", "org:admin"),
		{Name: "lang", Value: "en"},
	}
	code, hdr, body := serve(t, s, "GET", "/app", nil, nil, req...)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `lang="es"`, "the saved preference must win over a stale cookie")
	assert.Contains(t, hdr.Get("Set-Cookie"), "lang=es",
		"the cookie is re-synced so the pre-paint path and signed-out requests agree")
}

func TestQueryLangOverridesStoredLocale(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_loc2", "org_loc2", "org:admin")
	require.NoError(t, s.q.SetUserLocale(t.Context(), sqlc.SetUserLocaleParams{
		UserID: "user_loc2", Locale: "es",
	}))

	_, _, body := serve(t, s, "GET", "/app?lang=en", nil, nil,
		sessionCookie("user_loc2", "org_loc2", "org:admin"))
	assert.Contains(t, body, `lang="en"`, "?lang= is a deliberate per-request override")

	// …and it is NOT persisted: the next plain request is Spanish again.
	_, _, body = serve(t, s, "GET", "/app", nil, nil,
		sessionCookie("user_loc2", "org_loc2", "org:admin"))
	assert.Contains(t, body, `lang="es"`, "an override must not silently rewrite the saved choice")
}

func TestSetLocalePersistsToUserRow(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_loc3", "org_loc3", "org:admin")
	cookie := sessionCookie("user_loc3", "org_loc3", "org:admin")

	code, _, _ := postForm(t, s, "/set-locale",
		url.Values{"lang": {"es"}, "returnTo": {"/app/settings/account"}}, cookie)
	require.Equal(t, http.StatusOK, code)

	u, err := s.q.GetUserByID(t.Context(), "user_loc3")
	require.NoError(t, err)
	assert.Equal(t, "es", u.Locale, "the switcher must write the durable copy, not just a cookie")
}

// Clearing the choice restores browser detection rather than pinning English.
func TestSetLocaleEmptyClearsPreference(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_loc4", "org_loc4", "org:admin")
	cookie := sessionCookie("user_loc4", "org_loc4", "org:admin")
	require.NoError(t, s.q.SetUserLocale(t.Context(), sqlc.SetUserLocaleParams{
		UserID: "user_loc4", Locale: "es",
	}))

	code, hdr, _ := postForm(t, s, "/set-locale",
		url.Values{"lang": {""}, "returnTo": {"/app/settings/account"}}, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, hdr.Get("Set-Cookie"), "lang=;", "the cookie is expired, not set to en")

	u, err := s.q.GetUserByID(t.Context(), "user_loc4")
	require.NoError(t, err)
	assert.Empty(t, u.Locale, "empty means follow the browser")

	// With the preference gone, Accept-Language decides again.
	h := http.Header{}
	h.Set("Accept-Language", "es-ES,es;q=0.9")
	_, _, body := serve(t, s, "GET", "/app", nil, h,
		sessionCookie("user_loc4", "org_loc4", "org:admin"))
	// es-ES matches with a region extension (es-u-rg-eszzzz) — assert the
	// language subtag, not the exact tag the matcher chose.
	assert.Contains(t, body, `lang="es`)
}

// The server renders the dark class itself so a fresh device never flashes
// light before app.js runs.
func TestDarkThemeRendersOnHTMLServerSide(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th", "org_th", "org:admin")
	cookie := sessionCookie("user_th", "org_th", "org:admin")

	_, _, body := serve(t, s, "GET", "/app", nil, nil, cookie)
	assert.NotContains(t, body, `class="dark"`, "the default renders no theme class")

	require.NoError(t, s.q.SetUserTheme(t.Context(), sqlc.SetUserThemeParams{
		UserID: "user_th", Theme: "dark",
	}))
	_, hdr, body := serve(t, s, "GET", "/app", nil, nil, cookie)
	assert.Contains(t, body, `class="dark"`, "a saved dark theme paints dark on the first byte")
	assert.Contains(t, hdr.Get("Set-Cookie"), "theme=dark",
		"the cookie mirrors the row so the pre-paint script agrees")
}

// "system" must render nothing: only the browser knows the OS setting, and a
// server-rendered guess is exactly the flash this feature removes.
func TestSystemThemeRendersNoClass(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th2", "org_th2", "org:admin")
	require.NoError(t, s.q.SetUserTheme(t.Context(), sqlc.SetUserThemeParams{
		UserID: "user_th2", Theme: "system",
	}))
	_, _, body := serve(t, s, "GET", "/app", nil, nil,
		sessionCookie("user_th2", "org_th2", "org:admin"))
	assert.NotContains(t, body, `class="dark"`)
}

// The toggle posts the value it just applied locally; the server stores that
// value verbatim rather than inferring a flip it cannot get right for
// "system" (only the browser knows which way the OS points).
func TestSetThemePersistsPostedValue(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th3", "org_th3", "org:admin")
	cookie := sessionCookie("user_th3", "org_th3", "org:admin")

	code, hdr, _ := postForm(t, s, "/set-theme", url.Values{"theme": {"dark"}}, cookie)
	require.Equal(t, http.StatusNoContent, code, "the toggle already flipped the class; nothing to swap")
	assert.Contains(t, hdr.Get("Set-Cookie"), "theme=dark")
	u, err := s.q.GetUserByID(t.Context(), "user_th3")
	require.NoError(t, err)
	assert.Equal(t, "dark", u.Theme)

	code, hdr, _ = postForm(t, s, "/set-theme", url.Values{"theme": {"light"}}, cookie, &http.Cookie{Name: "theme", Value: "dark"})
	require.Equal(t, http.StatusNoContent, code)
	assert.Contains(t, hdr.Get("Set-Cookie"), "theme=light")
	u, err = s.q.GetUserByID(t.Context(), "user_th3")
	require.NoError(t, err)
	assert.Equal(t, "light", u.Theme)
}

// A missing value is a client bug, not an invitation to guess.
func TestSetThemeRequiresValue(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th7", "org_th7", "org:admin")

	code, _, _ := postForm(t, s, "/set-theme", url.Values{},
		sessionCookie("user_th7", "org_th7", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)
}

func TestSetThemeExplicitValueRedirects(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th4", "org_th4", "org:admin")
	cookie := sessionCookie("user_th4", "org_th4", "org:admin")

	code, _, _ := postForm(t, s, "/set-theme",
		url.Values{"theme": {"dark"}, "returnTo": {"/app/settings/account"}}, cookie)
	require.Equal(t, http.StatusOK, code, "a settings form gets a hard redirect so <html> re-renders")

	u, err := s.q.GetUserByID(t.Context(), "user_th4")
	require.NoError(t, err)
	assert.Equal(t, "dark", u.Theme)
}

func TestSetThemeRejectsUnknownValue(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th5", "org_th5", "org:admin")

	code, _, _ := postForm(t, s, "/set-theme", url.Values{"theme": {"neon"}},
		sessionCookie("user_th5", "org_th5", "org:admin"))
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	u, err := s.q.GetUserByID(t.Context(), "user_th5")
	require.NoError(t, err)
	assert.Equal(t, "system", u.Theme, "an unsupported value must not reach the CHECK constraint")
}

func TestAppearanceCardShowsCurrentChoices(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_th6", "org_th6", "org:admin")
	require.NoError(t, s.q.SetUserTheme(t.Context(), sqlc.SetUserThemeParams{
		UserID: "user_th6", Theme: "dark",
	}))
	_, _, body := serve(t, s, "GET", "/app/settings/account", nil, nil,
		sessionCookie("user_th6", "org_th6", "org:admin"))

	assert.Contains(t, body, `data-testid="appearance-card"`)
	for _, want := range []string{"theme-system", "theme-light", "theme-dark", "locale-pref-auto", "locale-pref-es"} {
		assert.Contains(t, body, want, "appearance control %q is missing", want)
	}
}

// The static tree ships inside the binary at a stable URL, so a cached
// app.js would keep running against a new server. That is not hypothetical:
// this feature moved client and server together.
func TestStaticAppAssetsRevalidate(t *testing.T) {
	s := integrationServer(t, nil)

	code, hdr, _ := serve(t, s, "GET", "/static/app.js", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "public, no-cache", hdr.Get("Cache-Control"),
		"a max-age here serves last release's JavaScript to this release's server")
	etag := hdr.Get("ETag")
	require.NotEmpty(t, etag, "revalidation needs a validator; embedded files have no modtime")

	// …and revalidation must be cheap, or "no-cache" just means "re-download".
	h := http.Header{}
	h.Set("If-None-Match", etag)
	code, _, body := serve(t, s, "GET", "/static/app.js", nil, h)
	assert.Equal(t, http.StatusNotModified, code)
	assert.Empty(t, body)
}

func TestVendoredAssetsStayImmutable(t *testing.T) {
	s := integrationServer(t, nil)
	_, hdr, _ := serve(t, s, "GET", "/static/vendor/htmx.min.js", nil, nil)
	assert.Contains(t, hdr.Get("Cache-Control"), "immutable",
		"vendored files are sha256-pinned at vendor time; their URL changes with their content")
}
