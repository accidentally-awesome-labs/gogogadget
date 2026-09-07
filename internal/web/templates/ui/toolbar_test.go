package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A toolbar role promises arrow-key navigation with one tab stop; these
// controls are each tabbable, so claiming it would advertise a contract that is
// not implemented.
func TestToolbarClaimsGroupNotToolbar(t *testing.T) {
	html := renderComponent(t, Toolbar(ToolbarOpts{Label: "Editor actions"}))
	assert.Contains(t, html, `role="group"`)
	assert.NotContains(t, html, `role="toolbar"`)
	assert.Contains(t, html, `aria-label="Editor actions"`)
}
