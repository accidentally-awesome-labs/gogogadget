package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A tooltip supplements a label; it never becomes one. The trigger is the
// caller's own element, so this renderer must not claim to name it.
func TestTooltipSupplementsRatherThanLabels(t *testing.T) {
	html := renderComponent(t, Tooltip(TooltipOpts{Text: "Copied to clipboard"}))
	assert.Contains(t, html, `role="tooltip"`)
	assert.NotContains(t, html, "aria-label=",
		"a tooltip that sets the accessible name replaces the control's real one")
}
