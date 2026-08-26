package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func gridOpts() DataGridOpts {
	return DataGridOpts{
		ID: "g", Caption: "Projects", BaseURL: "/projects", Target: "#content",
		Columns: []GridColumn{
			{Key: "name", Label: "Project", Sortable: true, Resizable: true, MinWidth: 120},
			{Key: "runs", Label: "Runs", Numeric: true, Sortable: true},
		},
		SortKey: "name", Sort: SortAsc,
		TotalRows: 400, RowCount: 50, Page: 1, TotalPages: 8,
	}
}

// The grid is a table. role="grid" promises a full two-dimensional keyboard
// model, and claiming it while behaving like a table leaves a screen-reader user
// worse off than the semantics they already know.
func TestDataGridKeepsTableSemantics(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	assert.Contains(t, html, "<table")
	assert.Contains(t, html, "<caption")
	assert.NotContains(t, html, `role="grid"`)
	assert.NotContains(t, html, `role="gridcell"`)
	assert.NotContains(t, html, `role="row"`)
}

// The pager is the reason windowed fetching is allowed to exist. Remove it and
// later rows are reachable only by scrolling, which excludes anyone who cannot.
func TestPagerSurvivesAlongsideWindowing(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	assert.Contains(t, html, `data-ui="pagination"`)
	assert.Contains(t, html, "/projects?page=2")
	// And the window size is published for the controller, not substituted for
	// the pager.
	assert.Contains(t, html, "data-grid-window")
}

// Exactly the sorted column claims a direction. Passing the table's direction to
// every header tells a screen reader all of them are sorted.
func TestOnlyTheSortedColumnClaimsADirection(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	require.Equal(t, 1, strings.Count(html, `aria-sort="ascending"`))
	assert.Equal(t, 0, strings.Count(html, `aria-sort="descending"`))
}

// Sorting is a link, so it works with no script and the sorted view is
// shareable. A click handler would make the sorted state unaddressable.
func TestSortingIsALink(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	assert.Contains(t, html, "<a href=")
	assert.Contains(t, html, "sort=")
	assert.NotContains(t, html, "onclick")
}

// The row count is text. A scrollbar reports "more rows" only to someone who
// can see it, and the total comes from the server because only a window is
// loaded.
func TestRowCountIsReadable(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	assert.Contains(t, html, "Showing 50 of 400 rows")
	assert.Contains(t, html, `aria-live="polite"`)
}

// Column controls do nothing without the controller, so shipping them visible
// promises behaviour a script-less page cannot deliver.
func TestColumnPickerIsHiddenUntilScripted(t *testing.T) {
	html := renderComponent(t, ColumnPicker(ColumnPickerOpts{
		For: "g", Columns: []GridColumn{{Key: "name", Label: "Project"}},
	}))

	assert.Contains(t, html, "hidden")
}

// Visibility is a set of independent toggles, which is what a checkbox group is.
// A custom menu would reimplement a keyboard model the platform already ships.
func TestColumnPickerUsesRealCheckboxes(t *testing.T) {
	html := renderComponent(t, ColumnPicker(ColumnPickerOpts{
		For: "g",
		Columns: []GridColumn{
			{Key: "name", Label: "Project"},
			{Key: "runs", Label: "Runs"},
		},
		Hidden: []string{"runs"},
	}))

	assert.Equal(t, 2, strings.Count(html, `type="checkbox"`))
	// The hidden column is unchecked, so the control reflects the state rather
	// than resetting it on every render.
	assert.Equal(t, 1, strings.Count(html, "checked"))
	assert.Contains(t, html, "<fieldset")
	assert.Contains(t, html, "<legend")
}

// A resize floor is required. Dragged to zero, a column hides its data with
// nothing left on screen to indicate the data exists.
func TestResizableColumnsCarryAFloor(t *testing.T) {
	html := renderComponent(t, DataGrid(gridOpts()))

	assert.Contains(t, html, `data-grid-resizable="true"`)
	assert.Contains(t, html, `data-grid-min-width="120"`)
	// An unspecified floor still gets one rather than defaulting to zero.
	assert.Contains(t, html, `data-grid-min-width="48"`)
}

// Capability flags are per column: advertising a resize handle on a column that
// cannot resize is a control that does nothing.
func TestCapabilitiesAreDeclaredPerColumn(t *testing.T) {
	html := renderComponent(t, DataGrid(DataGridOpts{
		ID: "g", Caption: "c", TotalRows: 1, RowCount: 1,
		Columns: []GridColumn{
			{Key: "a", Label: "A", Resizable: true},
			{Key: "b", Label: "B"},
		},
	}))

	assert.Equal(t, 1, strings.Count(html, `data-grid-resizable="true"`))
	assert.Equal(t, 1, strings.Count(html, `data-grid-resizable="false"`))
}

// Search is server-owned, like sort and paging: a client-side filter would hide
// rows the server never sent and report a count that is not the truth.
func TestGridSearchGoesToTheServer(t *testing.T) {
	html := renderComponent(t, GridToolbar(GridToolbarOpts{
		For: "g", SearchURL: "/projects", Target: "#content",
	}))

	assert.Contains(t, html, `hx-get="/projects"`)
	assert.Contains(t, html, `name="q"`)
}

// An empty grid keeps its toolbar and pager: losing the control that filtered
// the table to nothing leaves the user with no way back.
func TestEmptyGridKeepsItsChrome(t *testing.T) {
	opts := gridOpts()
	opts.RowCount = 0
	opts.Empty = EmptyState(EmptyStateOpts{Title: "No projects match"})
	html := renderComponent(t, DataGrid(opts))

	assert.Contains(t, html, "No projects match")
	assert.Contains(t, html, `data-ui="pagination"`)
	assert.Contains(t, html, "<thead")
}

// ColumnPicker.For must reach the DOM. A toolbar is often rendered beside the
// table rather than inside it, and without this link the controller cannot find
// the picker at all - it stays hidden and the column controls never appear.
func TestColumnPickerNamesItsGrid(t *testing.T) {
	html := renderComponent(t, ColumnPicker(ColumnPickerOpts{
		For: "projects-grid", Columns: []GridColumn{{Key: "name", Label: "Project"}},
	}))

	assert.Contains(t, html, `data-grid-for="projects-grid"`)
}

// The toolbar carries the same link, so search and column controls both address
// one named grid even with two grids on a page.
func TestGridToolbarNamesItsGrid(t *testing.T) {
	html := renderComponent(t, GridToolbar(GridToolbarOpts{For: "projects-grid"}))

	assert.Contains(t, html, `data-grid-for="projects-grid"`)
}
