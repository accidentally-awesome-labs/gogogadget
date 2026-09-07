package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A drawer is a dialog anchored to an edge. It must be a real <dialog> so the
// platform supplies the top layer, backdrop, focus trap and Escape - a
// hand-built panel would have to re-earn all four.
func TestDrawerIsANativeDialog(t *testing.T) {
	html := renderComponent(t, Drawer(DrawerOpts{ID: "filters", Title: "Filters", Side: SideRight}))
	assert.Contains(t, html, "<dialog")
	assert.Contains(t, html, `aria-labelledby="filters-title"`,
		"a modal with no accessible name is announced as just \"dialog\"")
	assert.Contains(t, html, `id="filters-title"`)
	assert.Contains(t, html, `<form method="dialog"`, "close must work without script")
	assert.Contains(t, html, "drawer-right")

	assert.Contains(t, renderComponent(t, Drawer(DrawerOpts{ID: "d", Title: "T", Side: SideLeft})), "drawer-left")
	// An unset side is the right edge, which is where a filter panel belongs in
	// a left-to-right layout.
	assert.Contains(t, renderComponent(t, Drawer(DrawerOpts{ID: "d", Title: "T"})), "drawer-right")
}
