package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// unrelated adjacent buttons.
func TestButtonGroupIsLabelled(t *testing.T) {
	group := renderComponent(t, ButtonGroup(ButtonGroupOpts{Label: "Row actions"}))
	assert.Contains(t, group, `aria-label="Row actions"`)
	assert.NotContains(t, group, "aria-pressed", "these are commands, not states")
}
