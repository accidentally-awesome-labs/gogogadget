package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// both remove it, which is the opposite of the point.
func TestVisuallyHiddenStaysInTheAccessibilityTree(t *testing.T) {
	html := renderComponent(t, VisuallyHidden(VisuallyHiddenOpts{}))
	assert.Contains(t, html, "sr-only")
	// Checking for the bare word "hidden" would match this component's own
	// data-ui name. The real requirements are that nothing removes it from the
	// accessibility tree.
	assert.NotContains(t, html, "aria-hidden")
	assert.NotContains(t, html, " hidden")
	assert.NotContains(t, html, "display:none")
}

// A group of controls needs a name, or assistive technology presents it as
