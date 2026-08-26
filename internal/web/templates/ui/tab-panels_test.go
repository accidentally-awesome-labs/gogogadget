package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// role="tablist" is a promise: one tab stop for the set, arrows to move,
// Home/End to jump, and each tab wired to its panel. Claiming the role without
// the contract is worse than plain buttons, because a screen-reader user then
// expects arrows to work.
func TestTabPanelsImplementsTheAriaTabContract(t *testing.T) {
	html := renderComponent(t, TabPanels(TabPanelsOpts{
		Label: "Environment", Selected: 1,
		Panels: []TabPanel{
			{ID: "dev", Label: "Development", Body: "d"},
			{ID: "stg", Label: "Staging", Body: "s"},
			{ID: "prd", Label: "Production", Body: "p"},
		},
	}))

	assert.Contains(t, html, `role="tablist"`)
	assert.Contains(t, html, `aria-label="Environment"`)
	assert.Equal(t, 3, strings.Count(html, `role="tab"`))
	assert.Equal(t, 3, strings.Count(html, `role="tabpanel"`))

	// Exactly one tab is in the tab order: the other two are removed from it,
	// which is what the roving tab stop means.
	assert.Equal(t, 2, strings.Count(html, `tabindex="-1"`),
		"a tablist where every tab is tabbable costs one Tab press per tab to escape")
	assert.Equal(t, 1, strings.Count(html, `aria-selected="true"`))

	// Each tab names its panel and each panel names its tab.
	assert.Contains(t, html, `aria-controls="stg"`)
	assert.Contains(t, html, `aria-labelledby="stg-tab"`)
}

// The declared native fallback is sequential panels, and the server renders it
// rather than the enhanced state: every panel open, in order, with the tab bar
// hidden. Hiding panels server-side would lose all but one with scripting off,
// and showing a tab bar only uiTabs can operate would offer dead buttons. This
// asserts the direction of the enhancement, which is the part a well-meaning
// change is most likely to reverse.
func TestTabPanelsServerRendersTheSequentialFallback(t *testing.T) {
	html := renderComponent(t, TabPanels(TabPanelsOpts{
		Label: "Environment", Selected: 1,
		Panels: []TabPanel{
			{ID: "dev", Label: "Development", Body: "first body"},
			{ID: "stg", Label: "Staging", Body: "second body"},
			{ID: "prd", Label: "Production", Body: "third body"},
		},
	}))

	// Every panel's content is present and none of them is hidden.
	for _, body := range []string{"first body", "second body", "third body"} {
		assert.Contains(t, html, body)
	}
	assert.Equal(t, 1, strings.Count(html, "hidden"),
		"the tab bar is the only hidden element; a hidden panel is content lost without script")
	assert.Contains(t, html, `data-ui-tablist hidden`)

	// The hooks uiTabs reveals and collapses through must be on the markup, or
	// the enhancement never happens and the page keeps the fallback forever.
	assert.Equal(t, 3, strings.Count(html, `class="tab" data-ui-tab>`))
	assert.Equal(t, 3, strings.Count(html, "data-ui-tabpanel"))
}
