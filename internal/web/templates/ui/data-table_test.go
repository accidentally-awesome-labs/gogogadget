package ui

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
)

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
	// Both slots are opaque to DataTable, so they stand in for whatever empty
	// state and toolbar the caller passes - the toolbar one carries the marker
	// a real TableToolbar publishes. Rendering EmptyState and TableToolbar here
	// would tie this payload to two modules a project can install DataTable
	// without.
	empty := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "No match")
		return err
	})
	toolbar := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<div data-ui="table-toolbar"></div>`)
		return err
	})

	html := renderComponent(t, DataTable(DataTableOpts{
		Caption: "Projects", Columns: []Column{{Key: "name", Label: "Name"}},
		RowCount: 0,
		Empty:    empty,
		Toolbar:  toolbar,
	}))
	assert.Contains(t, html, "No match")
	assert.Contains(t, html, `data-ui="table-toolbar"`)
	assert.NotContains(t, html, "<table", "no rows means no table to announce")
}
