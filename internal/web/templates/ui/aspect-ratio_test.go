package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// AspectRatio exists to stop layout shift: without a reserved box the page
// reflows when the medium arrives, moving whatever the user was about to click.
func TestAspectRatioReservesTheBox(t *testing.T) {
	assert.Contains(t, renderComponent(t, AspectRatio(AspectRatioOpts{Ratio: RatioVideo})), "aspect-video")
	assert.Contains(t, renderComponent(t, AspectRatio(AspectRatioOpts{Ratio: RatioSquare})), "aspect-square")

	// RatioAuto is the honest name for declining to reserve. Checking for the
	// bare prefix would match this component's own data-ui name.
	auto := renderComponent(t, AspectRatio(AspectRatioOpts{}))
	assert.NotContains(t, auto, "aspect-video")
	assert.NotContains(t, auto, "aspect-square")
	assert.NotContains(t, auto, `class="overflow-hidden "`,
		"an empty ratio must not leave a trailing space in the class list")
}
