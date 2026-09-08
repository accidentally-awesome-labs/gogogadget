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

// The drawer's id is a hub: aria-labelledby names an element derived from it,
// so whichever value the element ends up carrying has to be the value that
// derivation used. A caller who reached for Attrs.ID instead of ID once got
// both attributes on one <dialog>, and the browser kept the component's - so
// the drawer was labelled by an element that did not exist.
func TestDrawerLabelFollowsWhicheverIDTheCallerSupplied(t *testing.T) {
	declared := renderComponent(t, Drawer(DrawerOpts{
		ID: "filters", Title: "Filters", Attrs: Attrs{ID: "escape-hatch"},
	}))
	assert.Contains(t, declared, `id="filters"`, "the ID field must outrank the generic Attrs.ID")
	assert.Contains(t, declared, `aria-labelledby="filters-title"`)
	assert.NotContains(t, declared, "escape-hatch",
		"Attrs.ID stayed beside the id the drawer owns; two ids on one element is invalid HTML")

	// With no ID, Attrs.ID becomes the hub and the label has to follow it
	// there. A fallback that moved only the id attribute would leave
	// aria-labelledby pointing at "-title", which names nothing.
	fallback := renderComponent(t, Drawer(DrawerOpts{Title: "Filters", Attrs: Attrs{ID: "from-attrs"}}))
	assert.Contains(t, fallback, `id="from-attrs"`)
	assert.Contains(t, fallback, `aria-labelledby="from-attrs-title"`)
	assert.NotContains(t, fallback, `aria-labelledby="-title"`)
}
