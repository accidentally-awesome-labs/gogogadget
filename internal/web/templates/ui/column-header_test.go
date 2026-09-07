package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// aria-sort is what tells a screen-reader user which column orders the table
// and in which direction. Without it a sorted table is indistinguishable from
// an unsorted one, because the arrow glyph is invisible to them.
func TestColumnHeaderReportsSortState(t *testing.T) {
	col := Column{Key: "name", Label: "Name", Sortable: true}

	asc := renderComponent(t, ColumnHeader(ColumnHeaderOpts{Column: col, Sort: SortAsc, BaseURL: "/p", Target: "#t"}))
	assert.Contains(t, asc, `aria-sort="ascending"`)

	desc := renderComponent(t, ColumnHeader(ColumnHeaderOpts{Column: col, Sort: SortDesc, BaseURL: "/p", Target: "#t"}))
	assert.Contains(t, desc, `aria-sort="descending"`)

	// An unsorted column emits nothing: aria-sort="none" on every other header
	// is noise a screen reader reads out per column.
	none := renderComponent(t, ColumnHeader(ColumnHeaderOpts{Column: col, BaseURL: "/p", Target: "#t"}))
	assert.NotContains(t, none, "aria-sort")
	assert.Contains(t, none, `scope="col"`)
}

// Sorting must survive without htmx, and the sorted URL must be shareable.
func TestColumnHeaderSortIsARealLink(t *testing.T) {
	html := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column:  Column{Key: "name", Label: "Name", Sortable: true},
		BaseURL: "/app/projects", Target: "#table",
	}))
	assert.Contains(t, html, `href="/app/projects?sort=name&amp;dir=asc"`)
	assert.Contains(t, html, `hx-push-url="true"`)

	// A non-sortable column is plain text, not a dead link.
	plain := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "status", Label: "Status"}, BaseURL: "/app/projects",
	}))
	assert.NotContains(t, plain, "<a ")
}

// The third state is deliberate: without it a user who sorted by mistake cannot
// return the table to its natural order.
func TestSortURLCyclesThroughUnsorted(t *testing.T) {
	assert.Equal(t, "/p?sort=name&dir=asc", SortURL("/p", "", "name", SortNone))
	assert.Equal(t, "/p?sort=name&dir=desc", SortURL("/p", "", "name", SortAsc))
	assert.Equal(t, "/p", SortURL("/p", "", "name", SortDesc),
		"descending returns to the table's natural order")

	assert.Equal(t, "/p?q=go&order=name&dir=asc", SortURL("/p?q=go", "order", "name", SortNone),
		"an existing query string is preserved and the parameter name is the caller's")
}

// A wide table that can only scroll sideways on a phone puts data behind a
// gesture instead of choosing what matters.
func TestColumnHideBelowDropsColumnsOnSmallScreens(t *testing.T) {
	html := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "owner", Label: "Owner", HideBelow: BreakpointSM},
	}))
	assert.Contains(t, html, "hidden sm:table-cell")

	// Numeric columns right-align so digits line up by place value.
	numeric := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "runs", Label: "Runs", Numeric: true},
	}))
	assert.Contains(t, numeric, "text-right")
	assert.Contains(t, numeric, "tabular-nums")
}

// A caller who names no target wants plain link navigation. Emitting
// hx-target="" makes htmx intercept the click and swap into an empty selector,
// so the request lands nowhere and the href that would have worked is skipped.
func TestUntargetedColumnHeaderEmitsNoHTMX(t *testing.T) {
	const name = "column header"
	html := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "name", Label: "Name", Sortable: true}, BaseURL: "/x",
	}))

	assert.NotContains(t, html, `hx-target=""`, "%s emits an empty target", name)
	assert.NotContains(t, html, "hx-get", "%s should navigate, not swap", name)
	// The link controls keep their href - that is the whole point of dropping
	// the swap.
	assert.Contains(t, html, "href=", "%s must still navigate", name)
}

// A declared field is only a contract if a renderer honours it. Column.Width
// was declared, documented and populated while every renderer dropped it, so
// ui-core's shape check on the Column type - which is all ui-core can say,
// since it owns the type and not the renderers that read it - is what let that
// ship. Each consumer asserts its own honouring, here and in tree-grid_test.go.
func TestColumnHeaderHonoursTheDeclaredColumnWidth(t *testing.T) {
	// The trailing quote is deliberately not asserted: templ's style attribute
	// expression appends a semicolon and the attribute map does not, so the two
	// renderers differ by one character after the value.
	html := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "when", Label: "When", Width: "12rem"},
	}))
	assert.Contains(t, html, `style="width:12rem`, "ColumnHeader drops Column.Width")

	// The width is a length, not an inline-style hook: a declaration list would
	// let a caller reach past every rule the design system enforces on classes.
	bogus := renderComponent(t, ColumnHeader(ColumnHeaderOpts{
		Column: Column{Key: "k", Label: "K", Width: "8rem;position:fixed"},
	}))
	assert.NotContains(t, bogus, "position:fixed",
		"Width must carry a bare length, never a declaration list")
}
