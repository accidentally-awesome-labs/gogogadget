package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A declared field is only a contract if a renderer honours it. Column.Width
// was declared, documented and populated while every renderer dropped it, so
// ui-core's shape check on the Column type is what let that ship - and the
// shape check is all ui-core can say, because it owns the type and not the
// renderers that read it. Each consumer asserts its own honouring, here and in
// column-header_test.go.
//
// The trailing quote is deliberately not asserted: templ's style attribute
// expression appends a semicolon and the attribute map does not, so the two
// renderers differ by one character after the value.
func TestTreeGridHonoursTheDeclaredColumnWidth(t *testing.T) {
	html := renderComponent(t, TreeGrid(TreeGridOpts{
		ID: "effort", Label: "Effort", Columns: []Column{{Key: "when", Label: "When", Width: "12rem"}},
	}))
	assert.Contains(t, html, `style="width:12rem`, "TreeGrid drops Column.Width")
}
