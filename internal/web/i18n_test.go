package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/i18n"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

func init() {
	// Test-only key registered in English ONLY, to exercise the fallback path.
	message.SetString(language.English, "test.en_only", "Only English")
}

func TestLangParamRendersSpanish(t *testing.T) {
	s := integrationServer(t, nil)
	code, _, body := serve(t, s, "GET", "/?lang=es", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Lanza tu SaaS este fin de semana,")
	assert.Contains(t, body, `<html lang="es">`, "html lang follows the resolved tag")
}

func TestDefaultIsEnglish(t *testing.T) {
	s := integrationServer(t, nil)
	code, _, body := serve(t, s, "GET", "/", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Ship your SaaS this weekend,")
	assert.Contains(t, body, `<html lang="en">`)
}

func TestAcceptLanguageHeader(t *testing.T) {
	s := integrationServer(t, nil)
	h := http.Header{}
	h.Set("Accept-Language", "es-ES,es;q=0.9,en;q=0.5")
	code, _, body := serve(t, s, "GET", "/", nil, h)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Lanza tu SaaS este fin de semana,")

	// Unsupported preferred language falls through to the matcher default.
	h.Set("Accept-Language", "fr-FR,fr;q=0.9")
	code, _, body = serve(t, s, "GET", "/", nil, h)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Ship your SaaS this weekend,")
}

func TestCookieBeatsAcceptLanguage(t *testing.T) {
	s := integrationServer(t, nil)
	h := http.Header{}
	h.Set("Accept-Language", "es")
	cookie := &http.Cookie{Name: i18n.CookieName, Value: "en"}
	code, _, body := serve(t, s, "GET", "/", nil, h, cookie)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Ship your SaaS this weekend,", "cookie en wins over es header")
}

func TestMissingTranslationFallsBackToEnglish(t *testing.T) {
	ctx := i18n.WithTag(t.Context(), language.Spanish)
	assert.Equal(t, "Only English", i18n.T(ctx, "test.en_only"), "es miss retries the en printer")
	assert.Equal(t, "test.missing_everywhere", i18n.T(ctx, "test.missing_everywhere"), "missing everywhere renders the key — a visible bug, not a wrong string")
}

func TestSetLocaleSetsCookieAndRedirects(t *testing.T) {
	s := integrationServer(t, nil)
	token, csrfCookies := csrfFor(t, s)

	form := url.Values{"lang": {"es"}, "returnTo": {"/pricing"}}
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	code, hdr, _ := serve(t, s, "POST", "/set-locale", []byte(form.Encode()), h, csrfCookies...)
	require.Equal(t, http.StatusSeeOther, code)

	var cookie *http.Cookie
	for _, c := range hdr["Set-Cookie"] {
		if strings.HasPrefix(c, i18n.CookieName+"=") {
			cookie = parseSetCookie(t, c)
		}
	}
	require.NotNil(t, cookie, "ggg_lang cookie must be set")
	assert.Equal(t, "es", cookie.Value)
	assert.Equal(t, "/", cookie.Path)
	assert.True(t, cookie.HttpOnly)

	loc := hdr.Get("Location")
	require.Equal(t, "/pricing", loc)

	// Cookie now drives rendering.
	code, _, body := serve(t, s, "GET", "/", nil, nil, &http.Cookie{Name: i18n.CookieName, Value: "es"})
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Lanza tu SaaS este fin de semana,")
}

func TestSetLocaleValidation(t *testing.T) {
	s := integrationServer(t, nil)
	token, csrfCookies := csrfFor(t, s)
	h := http.Header{}
	h.Set("X-CSRF-Token", token)
	h.Set("Content-Type", "application/x-www-form-urlencoded")

	// Unsupported lang → 422.
	form := url.Values{"lang": {"klingon"}, "returnTo": {"/"}}
	code, _, _ := serve(t, s, "POST", "/set-locale", []byte(form.Encode()), h, csrfCookies...)
	assert.Equal(t, http.StatusUnprocessableEntity, code)

	// Off-origin returnTo (protocol-relative) → sanitized to "/".
	form = url.Values{"lang": {"es"}, "returnTo": {"//evil.com"}}
	code, hdr, _ := serve(t, s, "POST", "/set-locale", []byte(form.Encode()), h, csrfCookies...)
	require.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/", hdr.Get("Location"))

	// Absolute-URL returnTo → sanitized to "/" too.
	form = url.Values{"lang": {"es"}, "returnTo": {"https://evil.com"}}
	code, hdr, _ = serve(t, s, "POST", "/set-locale", []byte(form.Encode()), h, csrfCookies...)
	require.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/", hdr.Get("Location"))
}

func TestSetLocaleRequiresCSRF(t *testing.T) {
	s := integrationServer(t, nil)
	form := url.Values{"lang": {"es"}, "returnTo": {"/"}}
	h := http.Header{}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	code, _, _ := serve(t, s, "POST", "/set-locale", []byte(form.Encode()), h)
	assert.Equal(t, http.StatusForbidden, code, "set-locale is NOT csrf-exempt")
}

// TestTemplateKeysExistInCatalogs guards the merge: every i18n.T key used in a
// template must exist in BOTH catalogs (es parity included).
func TestTemplateKeysExistInCatalogs(t *testing.T) {
	sources, err := templSources()
	require.NoError(t, err)
	re := regexp.MustCompile(`i18n\.T\(ctx,\s*"([a-z0-9_.]+)"`)
	used := map[string]bool{}
	for _, src := range sources {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			used[m[1]] = true
		}
	}
	require.NotEmpty(t, used, "regex must find template keys")

	en := catalogKeys(t, language.English)
	es := catalogKeys(t, language.Spanish)

	for k := range used {
		assert.True(t, en[k], "key %s used in templates but missing from en catalog", k)
		assert.True(t, es[k], "key %s used in templates but missing from es catalog", k)
	}
}

func TestCatalogParity(t *testing.T) {
	en := catalogKeys(t, language.English)
	es := catalogKeys(t, language.Spanish)
	for k := range en {
		assert.True(t, es[k], "key %s missing from es catalog", k)
	}
	for k := range es {
		assert.True(t, en[k], "key %s missing from en catalog", k)
	}
}

// catalogKeys returns the keys PRESENT in a language's dictionary: a catalog
// key that formats as itself is missing from that language.
func catalogKeys(t *testing.T, tag language.Tag) map[string]bool {
	t.Helper()
	p := message.NewPrinter(tag)
	present := map[string]bool{}
	for k := range allCatalogKeys(t) {
		if p.Sprintf(k) != k {
			present[k] = true
		}
	}
	return present
}

// allCatalogKeys inventories the catalogs straight from their sources.
func allCatalogKeys(t *testing.T) map[string]bool {
	t.Helper()
	srcs, err := catalogSources()
	require.NoError(t, err)
	re := regexp.MustCompile(`message\.SetString\(language\.[A-Za-z]+,\s*"([a-z0-9_.]+)",`)
	keys := map[string]bool{}
	for _, src := range srcs {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			keys[m[1]] = true
		}
	}
	return keys
}

func templSources() ([]string, error) {
	entries, err := os.ReadDir("templates")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".templ") {
			b, err := os.ReadFile(filepath.Join("templates", e.Name()))
			if err != nil {
				return nil, err
			}
			out = append(out, string(b))
		}
	}
	return out, nil
}

func catalogSources() ([]string, error) {
	files := []string{
		"../i18n/catalog_en.go",
		"../i18n/catalog_es.go",
	}
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		out = append(out, string(b))
	}
	return out, nil
}

func parseSetCookie(t *testing.T, raw string) *http.Cookie {
	t.Helper()
	// Minimal parse of "name=value; Path=/; HttpOnly; ...".
	parts := strings.Split(raw, ";")
	kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
	require.Len(t, kv, 2)
	c := &http.Cookie{Name: kv[0], Value: kv[1]}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		switch {
		case p == "HttpOnly":
			c.HttpOnly = true
		case strings.HasPrefix(p, "Path="):
			c.Path = strings.TrimPrefix(p, "Path=")
		}
	}
	return c
}
