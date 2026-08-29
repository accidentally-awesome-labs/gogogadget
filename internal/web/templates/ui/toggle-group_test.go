package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// unrelated adjacent buttons.
func TestControlGroupsAreLabelled(t *testing.T) {
	toggles := renderComponent(t, ToggleGroup(ToggleGroupOpts{
		Label: "Density", Options: []ToggleOption{{Value: "cosy", Label: "Cosy", Selected: true}, {Value: "compact", Label: "Compact"}},
	}))
	assert.Contains(t, toggles, `role="group"`)
	assert.Contains(t, toggles, `aria-label="Density"`)
	assert.Contains(t, toggles, `aria-pressed="true"`)
	assert.Contains(t, toggles, `aria-pressed="false"`)
	assert.NotContains(t, toggles, `role="radiogroup"`,
		"radiogroup promises arrow-key navigation these buttons do not implement")

	group := renderComponent(t, ButtonGroup(ButtonGroupOpts{Label: "Row actions"}))
	assert.Contains(t, group, `aria-label="Row actions"`)
	assert.NotContains(t, group, "aria-pressed", "these are commands, not states")
}
