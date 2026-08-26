package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func panelOpts() PanelGroupOpts {
	return PanelGroupOpts{
		ID: "pg", PersistKey: "demo", Orientation: OrientationHorizontal,
		Panels: []PanelData{
			{ID: "left", Title: "List", Size: 35, MinSize: 20},
			{ID: "right", Title: "Detail", MinSize: 25},
		},
	}
}

// A pointer-only splitter is a control most people cannot use. role="separator"
// with a tab stop is what makes it operable by keyboard.
func TestHandleIsKeyboardOperable(t *testing.T) {
	html := renderComponent(t, PanelGroup(panelOpts()))

	assert.Contains(t, html, `role="separator"`)
	assert.Contains(t, html, `tabindex="0"`)
}

// A separator with no values reports a position of nothing.
func TestHandleReportsBoundedValues(t *testing.T) {
	html := renderComponent(t, PanelGroup(panelOpts()))

	assert.Contains(t, html, `aria-valuenow="35"`)
	assert.Contains(t, html, `aria-valuemin="20"`)
	// The max leaves room for the neighbour's floor, so dragging one panel can
	// never squeeze the next below its own minimum.
	assert.Contains(t, html, `aria-valuemax="75"`)
}

// A value outside its own bounds must be clamped, not reported. Reporting it
// would put the thumb somewhere the control cannot reach.
func TestOutOfRangeValueIsClamped(t *testing.T) {
	html := renderComponent(t, PanelHandle(PanelHandleOpts{
		ID: "h", Label: "Resize", Value: 200, Min: 10, Max: 80,
		Orientation: OrientationHorizontal,
	}))

	assert.Contains(t, html, `aria-valuenow="80"`)
	assert.NotContains(t, html, `aria-valuenow="200"`)
}

// Forty handles called "Resize" are indistinguishable in a screen reader's list,
// so each names the panels it sits between.
func TestHandleNamesThePanelsItSeparates(t *testing.T) {
	html := renderComponent(t, PanelGroup(panelOpts()))

	assert.Contains(t, html, `aria-label="Resize List and Detail"`)
}

// N panels need N-1 handles. An extra one at the end would resize nothing.
func TestHandleCountMatchesTheSplits(t *testing.T) {
	opts := panelOpts()
	opts.Panels = append(opts.Panels, PanelData{ID: "third", Title: "Third"})
	html := renderComponent(t, PanelGroup(opts))

	require.Equal(t, 2, strings.Count(html, `role="separator"`))
}

// The fallback is stacked panels, so the content is laid out and readable before
// any resizing exists - and that is also what a narrow screen gets.
func TestGroupStacksBeforeItSplits(t *testing.T) {
	html := renderComponent(t, PanelGroup(panelOpts()))

	assert.Contains(t, html, "flex-col")
	assert.Contains(t, html, "md:flex-row")
	// The vertical grip is hidden while stacked: a divider between full-width
	// blocks resizes nothing.
	assert.Contains(t, html, "hidden md:block")
}

// A panel with no declared size must still be visible; zero would hide it.
func TestUndeclaredSizeIsVisible(t *testing.T) {
	html := renderComponent(t, PanelGroup(PanelGroupOpts{
		ID: "pg", Panels: []PanelData{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}},
	}))

	assert.Contains(t, html, `aria-valuenow="50"`)
	assert.Contains(t, html, `aria-valuemin="10"`)
}

// Collapsible means a floor of zero; without it a panel keeps a minimum, because
// a panel dragged to nothing hides content with no sign it exists.
func TestOnlyCollapsiblePanelsMayReachZero(t *testing.T) {
	html := renderComponent(t, PanelGroup(PanelGroupOpts{
		ID: "pg", Panels: []PanelData{
			{ID: "a", Title: "A", Collapsible: true},
			{ID: "b", Title: "B"},
		},
	}))

	assert.Contains(t, html, `aria-valuemin="0"`)
}

// The panel's heading labels its region, so a screen-reader user can tell which
// pane they are in.
func TestPanelRegionIsLabelled(t *testing.T) {
	html := renderComponent(t, Panel(PanelOpts{Panel: PanelData{ID: "left", Title: "List"}}))

	assert.Contains(t, html, "<section")
	assert.Contains(t, html, `aria-labelledby="left-title"`)
	assert.Contains(t, html, `id="left-title"`)
}

// The orientation must be stated: arrow keys move along one axis, and a screen
// reader announces which.
func TestOrientationIsStated(t *testing.T) {
	html := renderComponent(t, PanelGroup(PanelGroupOpts{
		ID: "pg", Orientation: OrientationVertical,
		Panels: []PanelData{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}},
	}))

	assert.Contains(t, html, `aria-orientation="vertical"`)
}

// An unknown orientation normalizes rather than emitting nothing: a separator
// with no axis leaves the arrow keys undefined.
func TestInvalidOrientationNormalizes(t *testing.T) {
	html := renderComponent(t, PanelGroup(PanelGroupOpts{
		ID: "pg", Orientation: Orientation("sideways"),
		Panels: []PanelData{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}},
	}))

	assert.Contains(t, html, `aria-orientation="horizontal"`)
	assert.NotContains(t, html, "sideways")
}

// A panel must be able to hold something. Without declared content PanelGroup
// could only render titled empty boxes, which forced callers to hand-roll the
// group's root markup and Alpine hooks - two copies of a root that drifts the
// moment the group's classes change.
func TestPanelGroupRendersDeclaredContent(t *testing.T) {
	html := renderComponent(t, PanelGroup(PanelGroupOpts{
		ID: "pg", PersistKey: "demo",
		Panels: []PanelData{
			{ID: "left", Title: "List", Size: 40, Content: Text(TextOpts{Size: SizeSM})},
			{ID: "right", Title: "Detail"},
		},
	}))

	assert.Contains(t, html, `id="left"`)
	assert.Contains(t, html, `id="right"`)
	// The group still owns the root, so the controller and its persistence hook
	// are declared exactly once.
	assert.Equal(t, 1, strings.Count(html, `x-data="uiPanels"`))
	assert.Contains(t, html, `data-panel-persist="demo"`)
}
