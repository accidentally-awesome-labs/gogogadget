package web

import (
	"net/http"
	"strings"
	"testing"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/db/sqlc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func docsServer(t *testing.T) *Server {
	t.Helper()
	return integrationServer(t, func(d *Deps) {
		docs, err := content.LoadDocs(contentfs.FS, true)
		require.NoError(t, err)
		d.Docs = docs
	})
}

func TestDocsIndexRedirects(t *testing.T) {
	s := docsServer(t)
	code, hdr, _ := serve(t, s, "GET", "/docs", nil, nil)
	assert.Equal(t, http.StatusSeeOther, code)
	assert.Equal(t, "/docs/index", hdr.Get("Location"))
}

func TestDocsAllPagesRender(t *testing.T) {
	s := docsServer(t)
	for _, p := range s.docs.Pages {
		code, _, body := serve(t, s, "GET", "/docs/"+p.Slug, nil, nil)
		require.Equal(t, http.StatusOK, code, "/docs/%s", p.Slug)
		assert.Contains(t, body, "docs-page", "/docs/%s", p.Slug)
	}
}

func TestDocsSidebarOrderAndPrevNext(t *testing.T) {
	s := docsServer(t)
	code, _, body := serve(t, s, "GET", "/docs/getting-started", nil, nil)
	require.Equal(t, http.StatusOK, code)
	// Sidebar shows all four sections in order (match the section label markup,
	// not the nav's Features link).
	iStart := indexOf(body, ">Start</p>")
	iCore := indexOf(body, ">Core</p>")
	iFeatures := indexOf(body, ">Features</p>")
	iGuides := indexOf(body, ">Guides</p>")
	require.True(t, iStart >= 0 && iCore > iStart && iFeatures > iCore && iGuides > iFeatures,
		"sidebar sections out of order: %d %d %d %d", iStart, iCore, iFeatures, iGuides)
	// Prev/next footer: getting-started (2) sits between index (1) and architecture (3).
	assert.Contains(t, body, "/docs/index")
	assert.Contains(t, body, "/docs/architecture")
	// Edit link present.
	assert.Contains(t, body, "content/docs/getting-started.md")
}

func TestDocsUnknownSlug404(t *testing.T) {
	s := docsServer(t)
	code, _, _ := serve(t, s, "GET", "/docs/nope", nil, nil)
	assert.Equal(t, http.StatusNotFound, code)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestDocsSearchResults(t *testing.T) {
	s := docsServer(t)

	// Sidebar search box on every docs page.
	code, _, body := serve(t, s, "GET", "/docs/getting-started", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="docs-search-form"`, "search box in the docs sidebar")

	// A term that lives in titles/bodies: results page renders ranked hits.
	code, _, body = serve(t, s, "GET", "/docs/search?q=webhook", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="docs-search"`)
	assert.Contains(t, body, "Results for")
	assert.Contains(t, body, "/docs/webhooks", "webhooks page ranks for webhook")
	assert.Contains(t, body, `data-testid="docs-search-result"`)

	// AND semantics: a term pair that co-occurs nowhere matches nothing.
	code, _, body = serve(t, s, "GET", "/docs/search?q=webhook+kubernetes", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, `data-testid="docs-search-empty"`)

	// Empty query: the prompt state, not an error.
	code, _, body = serve(t, s, "GET", "/docs/search", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, body, "Type a term to search")
}

// changelogServer seeds the shipped release corpus into content_entries: the
// changelog is a database-backed content type now, and these tests assert on
// the page rendered from those rows.
func changelogServer(t *testing.T) (*Server, []content.Release) {
	t.Helper()
	s := integrationServer(t, nil)
	releases, err := content.ParseReleases(contentfs.FS)
	require.NoError(t, err)
	rows := make([]sqlc.CreateEntryParams, 0, len(releases))
	for _, r := range releases {
		rows = append(rows, sqlc.CreateEntryParams{
			Kind: "release", Slug: r.Slug, Title: r.Title, Summary: r.Summary,
			BodyMd: r.Body, BodyHtml: r.Body, Status: "published",
			PublishedAt: publishedAt(r.Date),
		})
	}
	seedEntries(t, s, rows...)
	return s, releases
}

func TestChangelogPageRendersEveryRelease(t *testing.T) {
	s, releases := changelogServer(t)
	code, _, body := serve(t, s, "GET", "/changelog", nil, nil)
	require.Equal(t, http.StatusOK, code)

	for _, r := range releases {
		assert.Contains(t, body, `id="`+r.Anchor+`"`, "release %s needs its anchor", r.Slug)
		assert.Contains(t, body, r.Title)
	}
	assert.Contains(t, body, "Everything that shipped")
}

// Newest-first is the contract a changelog reader relies on: they scan down
// until they reach the version they were on.
func TestChangelogPageOrdersNewestFirst(t *testing.T) {
	s, releases := changelogServer(t)
	_, _, body := serve(t, s, "GET", "/changelog", nil, nil)

	prev := -1
	for _, r := range releases {
		at := strings.Index(body, `id="`+r.Anchor+`"`)
		require.NotEqual(t, -1, at)
		assert.Greater(t, at, prev, "release %s renders out of order", r.Slug)
		prev = at
	}
}

// It is a public page: same chrome, indexable, and linked from the shell.
func TestChangelogIsPublicAndIndexable(t *testing.T) {
	s, _ := changelogServer(t)
	_, _, body := serve(t, s, "GET", "/changelog", nil, nil)

	assert.Contains(t, body, `<link rel="canonical" href="http://localhost:18080/changelog"`)
	assert.Contains(t, body, `hreflang="es"`)
	assert.Contains(t, body, `href="/changelog"`, "the nav links to it")
}

func TestSitemapIncludesChangelogWithLastmod(t *testing.T) {
	s, releases := changelogServer(t)
	_, _, body := serve(t, s, "GET", "/sitemap.xml", nil, nil)

	require.NotEmpty(t, releases)
	assert.Contains(t, body,
		"<loc>http://localhost:18080/changelog</loc><lastmod>"+releases[0].Date.UTC().Format("2006-01-02")+"</lastmod>",
		"the changelog is the one marketing page with a real modification date")
}

// An empty collection must render an empty page, not an error page: a fresh
// database that has not been seeded is a legitimate state.
func TestChangelogEmptyTableRendersEmptyPage(t *testing.T) {
	s := integrationServer(t, nil)
	_, err := s.db.Exec(t.Context(), "DELETE FROM content_entries WHERE kind = 'release'")
	require.NoError(t, err)
	s.cms.Invalidate()

	code, _, body := serve(t, s, "GET", "/changelog", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, body, "Something went wrong")

	code, hdr, sitemap := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, hdr.Get("Content-Type"), "application/xml")
	assert.Contains(t, sitemap, "<loc>http://localhost:18080/changelog</loc></url>",
		"a collection with no entries still has an index page")
}
