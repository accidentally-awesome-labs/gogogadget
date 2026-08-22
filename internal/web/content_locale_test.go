package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// localeServer seeds a shared entry plus a Spanish variant of the SAME slug,
// and a second entry that exists only in Spanish.
func localeServer(t *testing.T) *Server {
	t.Helper()
	s := integrationServer(t, nil)
	at := publishedAt(time.Now().Add(-time.Hour))
	seedEntries(t, s,
		sqlc.CreateEntryParams{
			Kind: "post", Slug: "loc-shared", Locale: "", Title: "Shared title",
			Summary: "Shared summary", BodyMd: "shared", BodyHtml: "<p>shared body</p>",
			Status: "published", PublishedAt: at,
		},
		sqlc.CreateEntryParams{
			Kind: "post", Slug: "loc-shared", Locale: "es", Title: "Titulo compartido",
			Summary: "Resumen compartido", BodyMd: "es", BodyHtml: "<p>cuerpo en espanol</p>",
			Status: "published", PublishedAt: at,
		},
		sqlc.CreateEntryParams{
			Kind: "post", Slug: "loc-only-es", Locale: "es", Title: "Solo espanol",
			BodyMd: "es", BodyHtml: "<p>solo</p>", Status: "published", PublishedAt: at,
		},
	)
	return s
}

// A variant REPLACES the shared row for readers in that language; it never
// doubles the slug on the index.
func TestLocaleVariantReplacesSharedEntry(t *testing.T) {
	s := localeServer(t)

	_, _, es := serve(t, s, "GET", "/blog?lang=es", nil, nil)
	assert.Contains(t, es, "Titulo compartido")
	assert.NotContains(t, es, "Shared title")
	assert.Equal(t, 1, strings.Count(es, `href="/blog/loc-shared"`), "one card per slug, not two")

	_, _, en := serve(t, s, "GET", "/blog?lang=en", nil, nil)
	assert.Contains(t, en, "Shared title")
	assert.NotContains(t, en, "Titulo compartido")
	assert.Equal(t, 1, strings.Count(en, `href="/blog/loc-shared"`))
}

func TestLocaleVariantServesItsOwnBody(t *testing.T) {
	s := localeServer(t)

	code, _, es := serve(t, s, "GET", "/blog/loc-shared?lang=es", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, es, "cuerpo en espanol")
	assert.NotContains(t, es, "shared body")

	code, _, en := serve(t, s, "GET", "/blog/loc-shared?lang=en", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, en, "shared body")
}

// An entry that exists ONLY in Spanish is genuinely absent in English —
// falling back to a Spanish page for an English reader would be worse than
// a 404.
func TestSpanishOnlyEntryIsAbsentInEnglish(t *testing.T) {
	s := localeServer(t)

	_, _, en := serve(t, s, "GET", "/blog?lang=en", nil, nil)
	assert.NotContains(t, en, "Solo espanol")
	code, _, _ := serve(t, s, "GET", "/blog/loc-only-es?lang=en", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)

	_, _, es := serve(t, s, "GET", "/blog?lang=es", nil, nil)
	assert.Contains(t, es, "Solo espanol")
	code, _, _ = serve(t, s, "GET", "/blog/loc-only-es?lang=es", nil, nil)
	assert.Equal(t, http.StatusOK, code)
}

// Language versions are expressed through hreflang, not duplicate <loc>
// entries: the sitemap resolves at the default locale.
func TestSitemapListsEachSlugOnce(t *testing.T) {
	s := localeServer(t)
	_, _, sitemap := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	assert.Equal(t, 1, strings.Count(sitemap, "<loc>http://localhost:18080/blog/loc-shared</loc>"))
	assert.NotContains(t, sitemap, "/blog/loc-only-es",
		"a Spanish-only entry has no default-locale URL to advertise")
}

// The feed is one canonical set of items rather than something that shifts
// with a reader's cookie.
func TestFeedUsesTheDefaultLocale(t *testing.T) {
	s := localeServer(t)
	_, _, feed := serve(t, s, "GET", "/rss.xml", nil, nil)
	assert.Contains(t, feed, "Shared title")
	assert.NotContains(t, feed, "Titulo compartido")
}
