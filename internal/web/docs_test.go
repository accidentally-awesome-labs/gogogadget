package web

import (
	"net/http"
	"testing"

	contentfs "github.com/gogogadget/gogogadget/content"
	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func docsServer(t *testing.T) *Server {
	t.Helper()
	return integrationServer(t, func(d *Deps) {
		blog, err := content.LoadBlog(contentfs.FS, true)
		require.NoError(t, err)
		docs, err := content.LoadDocs(contentfs.FS, true)
		require.NoError(t, err)
		d.Blog = blog
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
