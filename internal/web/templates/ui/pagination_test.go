package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two pagers can share a screen - a list of projects and a list of its files -
// so the parameter name is a caller decision. Hardcoding "page" makes one pager
// move the other.
func TestPaginationUsesTheCallersParameter(t *testing.T) {
	assert.Equal(t, "/app?page=2", PageURL("/app", "", 2), "the default stays page")
	assert.Equal(t, "/app?files=2", PageURL("/app", "files", 2))
	assert.Equal(t, "/app?q=go&files=2", PageURL("/app?q=go", "files", 2),
		"an existing query string must be preserved")

	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app/files", Param: "files", Target: "#t",
	}))
	assert.Contains(t, html, "files=1")
	assert.Contains(t, html, "files=3")
	assert.NotContains(t, html, "page=")
}

// A pager must survive without htmx: real hrefs make it work with scripts
// disabled, middle-clickable, and copyable after a pushed URL.
func TestPaginationKeepsRealLinks(t *testing.T) {
	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app", Target: "#t",
	}))
	assert.Contains(t, html, `href="/app?page=1"`)
	assert.Contains(t, html, `hx-get="/app?page=1"`)
	assert.Contains(t, html, `hx-swap="innerMorph"`)
	assert.Contains(t, html, `hx-push-url="true"`)

	// One page is not a pager.
	assert.Empty(t, renderComponent(t, Pagination(PaginationOpts{Page: 1, TotalPages: 1, BaseURL: "/app"})))
}

// The window keeps the first and last page - the two destinations users reach
// for - and elides the rest, so a hundred pages do not mean a hundred links.
func TestPageWindowElidesTheMiddle(t *testing.T) {
	assert.Equal(t, []int{1, 2, 3, 4, 5}, PageWindow(3, 5), "a short pager shows every page")
	assert.Equal(t, []int{1, 0, 49, 50, 51, 0, 100}, PageWindow(50, 100))
	assert.Equal(t, []int{1, 2, 0, 100}, PageWindow(1, 100), "the first page has no left neighbour")
	assert.Equal(t, []int{1, 0, 99, 100}, PageWindow(100, 100))

	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 50, TotalPages: 100, BaseURL: "/app", Target: "#t", Numbered: true,
	}))
	require.Contains(t, html, "…")
	assert.Contains(t, html, `aria-current="page"`)
	assert.Contains(t, html, `aria-label="Page 49"`,
		"a bare digit is ambiguous in a row of numbers")
	assert.Equal(t, 2, strings.Count(html, "…"))
}
