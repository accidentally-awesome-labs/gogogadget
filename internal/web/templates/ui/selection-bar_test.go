package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
