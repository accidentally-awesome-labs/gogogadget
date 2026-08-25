package ui

import (
	"strings"
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

// DataTable composes the surface without owning rows, and only the sorted
// column may claim a direction.
func TestDataTableMarksOnlyTheSortedColumn(t *testing.T) {
	html := renderComponent(t, DataTable(DataTableOpts{
		Caption: "Projects",
		Columns: []Column{
			{Key: "name", Label: "Name", Sortable: true},
			{Key: "runs", Label: "Runs", Sortable: true},
		},
		SortKey: "name", SortDir: SortDesc,
		BaseURL: "/p", Target: "#t", RowCount: 1,
	}))
	assert.Equal(t, 1, strings.Count(html, "aria-sort="),
		"passing the table's direction to every header would mark all of them sorted")
	assert.Contains(t, html, `aria-sort="descending"`)
	assert.Contains(t, html, "<caption")
	assert.Contains(t, html, "Projects")
}

// A filtered-to-nothing table must keep its toolbar and pager, or the control
// that caused the empty result disappears with the rows.
func TestDataTableKeepsItsControlsWhenEmpty(t *testing.T) {
	html := renderComponent(t, DataTable(DataTableOpts{
		Caption: "Projects", Columns: []Column{{Key: "name", Label: "Name"}},
		RowCount: 0,
		Empty:    EmptyState(EmptyStateOpts{Body: "No match", Variant: EmptyInline}),
		Toolbar:  TableToolbar(TableToolbarOpts{Label: "Controls"}),
	}))
	assert.Contains(t, html, "No match")
	assert.Contains(t, html, `data-ui="table-toolbar"`)
	assert.NotContains(t, html, "<table", "no rows means no table to announce")
}

// The count changes as the user selects, and an unannounced count means a
// screen-reader user cannot tell how many rows a bulk delete will affect.
func TestSelectionBarAnnouncesTheCount(t *testing.T) {
	html := renderComponent(t, SelectionBar(SelectionBarOpts{
		Count: 3, CountLabel: "3 projects selected", ClearURL: "/p", Target: "#t",
	}))
	assert.Contains(t, html, `role="status"`)
	assert.Contains(t, html, "3 projects selected")
	assert.Contains(t, html, "Clear selection")

	// Nothing selected means nothing to show.
	assert.Empty(t, renderComponent(t, SelectionBar(SelectionBarOpts{})))
}

// "Actions" repeated on forty rows gives a screen-reader user forty identical
// menus with no way to tell which row each belongs to.
func TestRowActionsNamesItsRow(t *testing.T) {
	html := renderComponent(t, RowActions(RowActionsOpts{
		Label: "Actions for Apollo", Items: []MenuItem{{Label: "Rename", Href: "/x"}},
	}))
	assert.Contains(t, html, "Actions for Apollo")
	assert.Contains(t, html, "data-ui-menu-trigger")
}
