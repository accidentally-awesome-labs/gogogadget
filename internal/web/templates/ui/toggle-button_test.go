package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// aria-pressed states that a control is a toggle, and a toggle that does not
// report its state is indistinguishable from a plain button to a screen-reader
// user. The other half of the claim - that a plain button never claims the
// attribute - is asserted by each of those buttons in its own payload.
func TestToggleButtonReportsItsPressedState(t *testing.T) {
	assert.Contains(t, renderComponent(t, ToggleButton(ToggleButtonOpts{Label: "Grid", On: true})),
		`aria-pressed="true"`)
	assert.Contains(t, renderComponent(t, ToggleButton(ToggleButtonOpts{Label: "Grid"})),
		`aria-pressed="false"`,
		"an off toggle must still report its state, or only the on state is announced")
}
