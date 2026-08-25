package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A panel that only opens on hover is unreachable by keyboard and absent on
// touch, where there is no hover state at all. The trigger is focusable and the
// behaviour is bound to focus as well as hover.
func TestHoverCardIsReachableWithoutAPointer(t *testing.T) {
	html := renderComponent(t, HoverCard(HoverCardOpts{ID: "preview", Label: "Ada Lovelace"}))
	assert.Contains(t, html, `tabindex="0"`, "the trigger must be focusable")
	assert.Contains(t, html, `x-data="uiHoverCard"`)

	// The preview describes the trigger; it does not replace its name. A
	// paragraph of preview text as the accessible name makes the link
	// unreadable in a screen reader's link list.
	assert.Contains(t, html, `aria-describedby="preview"`)
	assert.NotContains(t, html, "aria-labelledby")
	assert.Contains(t, html, `id="preview"`)
	assert.Contains(t, html, "Ada Lovelace")
}

// A tooltip supplements a label; it never becomes one. The trigger is the
// caller's own element, so this renderer must not claim to name it.
func TestTooltipSupplementsRatherThanLabels(t *testing.T) {
	html := renderComponent(t, Tooltip(TooltipOpts{Text: "Copied to clipboard"}))
	assert.Contains(t, html, `role="tooltip"`)
	assert.NotContains(t, html, "aria-label=",
		"a tooltip that sets the accessible name replaces the control's real one")
}

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
