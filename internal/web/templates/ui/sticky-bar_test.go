package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Sticky rather than fixed: a fixed bar overlays the viewport, and on a short
// screen it can hide the very field its Save button submits.
func TestStickyBarStaysInItsContainer(t *testing.T) {
	html := renderComponent(t, StickyBar(StickyBarOpts{Side: SideBottom}))
	assert.Contains(t, html, "sticky")
	assert.NotContains(t, html, "fixed")
	assert.Contains(t, html, "bottom-0")

	assert.Contains(t, renderComponent(t, StickyBar(StickyBarOpts{Side: SideTop})), "top-0")
}
