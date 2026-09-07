package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// unrelated adjacent buttons.
func TestToggleGroupIsLabelled(t *testing.T) {
	toggles := renderComponent(t, ToggleGroup(ToggleGroupOpts{
		Label: "Density", Options: []ToggleOption{{Value: "cosy", Label: "Cosy", Selected: true}, {Value: "compact", Label: "Compact"}},
	}))
	assert.Contains(t, toggles, `role="group"`)
	assert.Contains(t, toggles, `aria-label="Density"`)
	assert.Contains(t, toggles, `aria-pressed="true"`)
	assert.Contains(t, toggles, `aria-pressed="false"`)
	assert.NotContains(t, toggles, `role="radiogroup"`,
		"radiogroup promises arrow-key navigation these buttons do not implement")
}
