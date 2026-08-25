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

	// Unselected panels are hidden, not merely invisible: an invisible panel
	// left in the tab order is a focus trap.
	assert.Equal(t, 2, strings.Count(html, "hidden"))
}
