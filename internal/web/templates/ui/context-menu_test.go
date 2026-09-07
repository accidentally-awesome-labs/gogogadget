package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Right-click is unreachable by keyboard, so a context menu without a visible,
// focusable trigger is a set of commands a keyboard user cannot invoke.
func TestContextMenuKeepsAVisibleTrigger(t *testing.T) {
	html := renderComponent(t, ContextMenu(ContextMenuOpts{
		Label: "Row actions", Items: []MenuItem{{Label: "Rename", Href: "/x"}},
	}))
	assert.Contains(t, html, "data-ui-context-trigger")
	assert.Contains(t, html, "data-ui-menu-trigger",
		"the keyboard path is the same menu every other trigger uses")
	// The trigger's accessible name arrives as visually hidden text, which is
	// how DropdownMenu names an icon-only control.
	assert.Contains(t, html, "Row actions")
	assert.Contains(t, html, "sr-only")
	assert.NotContains(t, html, "oncontextmenu",
		"no inline handlers: CSP forbids them and the behaviour belongs to the fragment")
}
