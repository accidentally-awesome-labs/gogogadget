package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Two panes side by side on a phone give each about twenty characters per line.
func TestSplitStacksOnSmallScreens(t *testing.T) {
	html := renderComponent(t, Split(SplitOpts{Ratio: RatioWide, Gap: GapMD}))
	assert.Contains(t, html, "grid-cols-1", "one column is the small-screen default")
	assert.Contains(t, html, "md:grid-cols-3", "the ratio only applies from md up")
	assert.Contains(t, html, "gap-4")
}
