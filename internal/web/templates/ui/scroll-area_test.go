package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A scrollable div with no tab stop is reachable only by pointer, so once its
// content overflows it is unreadable to a keyboard user: there is nothing for
// the arrow keys to act on.
func TestScrollAreaIsKeyboardScrollable(t *testing.T) {
	html := renderComponent(t, ScrollArea(ScrollAreaOpts{Label: "Release notes", Height: HeightSM}))
	assert.Contains(t, html, `tabindex="0"`)
	assert.Contains(t, html, `role="region"`)
	assert.Contains(t, html, `aria-label="Release notes"`)
	assert.Contains(t, html, "overflow-y-auto")
	assert.Contains(t, html, "max-h-32")
}
