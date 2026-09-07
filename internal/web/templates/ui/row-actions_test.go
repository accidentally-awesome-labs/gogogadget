package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// "Actions" repeated on forty rows gives a screen-reader user forty identical
// menus with no way to tell which row each belongs to.
func TestRowActionsNamesItsRow(t *testing.T) {
	html := renderComponent(t, RowActions(RowActionsOpts{
		Label: "Actions for Apollo", Items: []MenuItem{{Label: "Rename", Href: "/x"}},
	}))
	assert.Contains(t, html, "Actions for Apollo")
	assert.Contains(t, html, "data-ui-menu-trigger")
}
