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

// aria-label overrides text content, so labelling every link "Page N" left the
// prev and next controls announcing as "Page 1"/"Page 3" — indistinguishable
// from the numbered link beside them, with the direction they exist to convey
// destroyed. axe never caught it: it asserts a name is PRESENT, never that it
// is right.
func TestPrevAndNextKeepTheirOwnAccessibleNames(t *testing.T) {
	labels := PagerLabels{
		Prev: func(int, int) string { return "← Previous" },
		Next: func(int, int) string { return "Next →" },
	}
	opts := func(numbered bool) PaginationOpts {
		return PaginationOpts{
			Page: 2, TotalPages: 5, BaseURL: "/app", Target: "#t",
			Numbered: numbered, Labels: labels,
		}
	}

	// The direction controls carry no aria-label at all, in either layout, so
	// their visible text IS the accessible name.
	for _, numbered := range []bool{false, true} {
		html := renderComponent(t, Pagination(opts(numbered)))
		assert.Contains(t, html, `class="btn btn-ghost">← Previous</a>`)
		assert.Contains(t, html, `class="btn btn-ghost">Next →</a>`)
	}

	// Page 2 of 5 with no numbers is two links, neither of them a bare digit,
	// so no link needs naming after a page.
	assert.NotContains(t, renderComponent(t, Pagination(opts(false))), `aria-label="Page`)

	// Numbered adds links to 1, 3, 4 and 5 — four bare digits, four labels, and
	// still none on prev or next.
	html := renderComponent(t, Pagination(opts(true)))
	assert.Equal(t, 4, strings.Count(html, `aria-label="Page `),
		"exactly one label per bare digit, and none on the direction controls")
	assert.Contains(t, html, `aria-label="Page 4"`)
}

// A caller who names no target wants plain link navigation. Emitting
// hx-target="" makes htmx intercept the click and swap into an empty selector,
// so the request lands nowhere and the href that would have worked is skipped.
func TestUntargetedPaginationEmitsNoHTMX(t *testing.T) {
	const name = "pagination"
	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/x",
	}))

	assert.NotContains(t, html, `hx-target=""`, "%s emits an empty target", name)
	assert.NotContains(t, html, "hx-get", "%s should navigate, not swap", name)
	// The link controls keep their href - that is the whole point of dropping
	// the swap.
	assert.Contains(t, html, "href=", "%s must still navigate", name)
}

// With a target the htmx contract is unchanged: these components are the
// product's paging and sorting surface and must keep swapping.
func TestTargetedNavigationStillSwaps(t *testing.T) {
	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/x", Target: "#content",
	}))

	assert.Contains(t, html, `hx-target="#content"`)
	assert.Contains(t, html, "hx-get")
	assert.Contains(t, html, `hx-swap="innerMorph"`)
}

// PagerLabels holds functions, so an omitted label used to nil-dereference and
// take down the whole page render. A missing translation must degrade to a
// readable fallback instead: an English arrow is a translation bug, a panic is
// an outage.
func TestPaginationSurvivesMissingLabels(t *testing.T) {
	html := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app/projects", Target: "#table",
	}))

	assert.Contains(t, html, `aria-label="Pagination"`,
		"the landmark still needs a name when no localized one is supplied")
	assert.Contains(t, html, "Previous")
	assert.Contains(t, html, "Next")
	assert.Contains(t, html, "2 / 5")

	// A supplied label must still win over every fallback.
	localized := renderComponent(t, Pagination(PaginationOpts{
		Page: 2, TotalPages: 5, BaseURL: "/app/projects", Target: "#table",
		Labels: PagerLabels{
			Aria:   func(int, int) string { return "Paginación" },
			Prev:   func(int, int) string { return "Anterior" },
			Next:   func(int, int) string { return "Siguiente" },
			PageOf: func(p, n int) string { return "Página 2 de 5" },
		},
	}))
	assert.Contains(t, localized, `aria-label="Paginación"`)
	assert.Contains(t, localized, "Anterior")
	assert.NotContains(t, localized, "Previous")
}
