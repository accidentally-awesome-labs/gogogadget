package web

import (
	"encoding/json"
	"encoding/xml"

	"github.com/gogogadget/gogogadget/internal/content"
	"github.com/gogogadget/gogogadget/internal/web/templates"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	canonicalRe = regexp.MustCompile(`<link rel="canonical" href="([^"]+)"`)
	alternateRe = regexp.MustCompile(`<link rel="alternate" hreflang="([^"]+)" href="([^"]+)"`)
	jsonLDRe    = regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`)
)

func canonicalOf(t *testing.T, body string) string {
	t.Helper()
	m := canonicalRe.FindStringSubmatch(body)
	require.Len(t, m, 2, "page must carry a canonical link")
	return m[1]
}

// The locale switcher made every public page reachable at /, /?lang=en and
// /?lang=es. Without canonicals a crawler indexes three URLs with the same
// content; with them, each language version collapses to exactly one.
func TestCanonicalCollapsesDuplicateURLs(t *testing.T) {
	s := integrationServer(t, nil)

	for _, tc := range []struct{ request, want string }{
		{"/pricing", "http://localhost:18080/pricing"},
		{"/pricing?lang=en", "http://localhost:18080/pricing"},            // default locale owns the bare path
		{"/pricing?lang=es", "http://localhost:18080/pricing?lang=es"},    // …a translation is its own URL
		{"/pricing?utm_source=x&ref=y", "http://localhost:18080/pricing"}, // tracking junk cannot fork the page
		{"/pricing?lang=es&utm_source=x", "http://localhost:18080/pricing?lang=es"},
		{"/pricing?lang=klingon", "http://localhost:18080/pricing"}, // unsupported → the default
	} {
		_, _, body := serve(t, s, "GET", tc.request, nil, nil)
		assert.Equal(t, tc.want, canonicalOf(t, body), "canonical for %s", tc.request)
	}
}

// hreflang must be reciprocal and name every language plus x-default, or
// search engines ignore the whole set.
func TestHreflangAlternatesAreCompleteAndReciprocal(t *testing.T) {
	s := integrationServer(t, nil)

	collect := func(path string) map[string]string {
		_, _, body := serve(t, s, "GET", path, nil, nil)
		out := map[string]string{}
		for _, m := range alternateRe.FindAllStringSubmatch(body, -1) {
			out[m[1]] = m[2]
		}
		return out
	}

	want := map[string]string{
		"en":        "http://localhost:18080/blog",
		"es":        "http://localhost:18080/blog?lang=es",
		"x-default": "http://localhost:18080/blog",
	}
	assert.Equal(t, want, collect("/blog"))
	assert.Equal(t, want, collect("/blog?lang=es"),
		"every version must advertise the SAME set — a one-way hreflang is discarded")
}

func TestFeedIsDiscoverableFromPages(t *testing.T) {
	s := integrationServer(t, nil)
	_, _, body := serve(t, s, "GET", "/blog", nil, nil)
	assert.Contains(t, body, `<link rel="alternate" type="application/rss+xml"`,
		"the feed has always existed; without this link nothing can find it")
	assert.Contains(t, body, `href="http://localhost:18080/rss.xml"`)
}

// Authed surfaces are not indexable, so canonical/hreflang there is noise.
func TestNoCanonicalOnAuthedPages(t *testing.T) {
	s := integrationServer(t, nil)
	seedMembership(t, s, "user_seo", "org_seo", "org:admin")
	_, _, body := serve(t, s, "GET", "/app", nil, nil, sessionCookie("user_seo", "org_seo", "org:admin"))
	assert.NotContains(t, body, `rel="canonical"`)
	assert.NotContains(t, body, `hreflang=`)
}

func TestHomeStructuredData(t *testing.T) {
	s := integrationServer(t, nil)
	_, _, body := serve(t, s, "GET", "/", nil, nil)

	m := jsonLDRe.FindStringSubmatch(body)
	require.Len(t, m, 2, "home must carry a JSON-LD block")

	var blocks []map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(m[1])), &blocks),
		"structured data must be valid JSON or search engines skip it")
	types := []string{}
	for _, b := range blocks {
		types = append(types, b["@type"].(string))
		assert.Equal(t, "https://schema.org", b["@context"])
	}
	assert.ElementsMatch(t, []string{"Organization", "WebSite"}, types)

	// The SearchAction must point at a search endpoint that actually exists.
	for _, b := range blocks {
		if b["@type"] != "WebSite" {
			continue
		}
		action := b["potentialAction"].(map[string]any)
		target := action["target"].(string)
		assert.Contains(t, target, "/docs/search?q={search_term_string}")
		code, _, _ := serve(t, s, "GET", "/docs/search?q=webhook", nil, nil)
		assert.Equal(t, http.StatusOK, code, "the advertised search endpoint must respond")
	}
}

func TestBlogPostStructuredData(t *testing.T) {
	s := docsServer(t) // real blog + docs, not the empty fixtures
	require.NotEmpty(t, s.blog.Posts)
	post := s.blog.Posts[0]

	_, _, body := serve(t, s, "GET", "/blog/"+post.Slug, nil, nil)
	m := jsonLDRe.FindStringSubmatch(body)
	require.Len(t, m, 2)

	var ld map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(m[1])), &ld))
	assert.Equal(t, "BlogPosting", ld["@type"])
	assert.Equal(t, post.Title, ld["headline"])
	assert.Equal(t, post.Date.UTC().Format("2006-01-02"), ld["datePublished"])
	assert.Equal(t, "http://localhost:18080/blog/"+post.Slug, ld["url"])
	assert.NotEmpty(t, ld["author"], "Google requires an author on BlogPosting")
	assert.NotEmpty(t, ld["publisher"])
}

// A title containing markup must not be able to close the data block.
func TestStructuredDataEscapesMarkup(t *testing.T) {
	s := integrationServer(t, nil)
	// Render a page whose structured data contains markup and confirm the
	// element cannot be closed early.
	var b strings.Builder
	err := templates.LDScript(
		s.postJSONLD(content.Post{Title: "</script><script>alert(1)</script>", Slug: "x", Author: "A"}),
	).Render(t.Context(), &b)
	require.NoError(t, err)
	out := b.String()
	assert.Equal(t, 1, strings.Count(out, "</script>"), "only the element's own closing tag")
	assert.NotContains(t, out, "<script>alert")
}

func TestSitemapCarriesLastmodForDatedContent(t *testing.T) {
	s := docsServer(t)
	code, hdr, body := serve(t, s, "GET", "/sitemap.xml", nil, nil)
	require.Equal(t, http.StatusOK, code)
	assert.Contains(t, hdr.Get("Content-Type"), "application/xml")

	var doc struct {
		URLs []struct {
			Loc     string `xml:"loc"`
			LastMod string `xml:"lastmod"`
		} `xml:"url"`
	}
	require.NoError(t, xml.Unmarshal([]byte(body), &doc), "sitemap must be well-formed XML")
	require.NotEmpty(t, doc.URLs)

	byLoc := map[string]string{}
	for _, u := range doc.URLs {
		byLoc[u.Loc] = u.LastMod
	}
	post := s.blog.Posts[0]
	assert.Equal(t, post.Date.UTC().Format("2006-01-02"),
		byLoc["http://localhost:18080/blog/"+post.Slug], "posts carry the date they actually have")
	assert.Empty(t, byLoc["http://localhost:18080/pricing"],
		"pages with no real date carry no lastmod — a fabricated one teaches crawlers to ignore the field")
}
