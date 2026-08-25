package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A screen reader announcing "loading" for every grey box is worse than
// silence; the region's own live announcement reports arrival.
func TestSkeletonIsAlwaysHiddenFromAssistiveTechnology(t *testing.T) {
	single := renderComponent(t, Skeleton(SkeletonOpts{}))
	assert.Contains(t, single, `aria-hidden="true"`)
	assert.NotContains(t, single, `role="status"`)
	assert.NotContains(t, single, "aria-live")
	assert.NotContains(t, single, "Loading")

	lines := renderComponent(t, Skeleton(SkeletonOpts{Lines: 3}))
	assert.Equal(t, 3, strings.Count(lines, "animate-pulse"))
	assert.Contains(t, lines, "w-2/3", "the last line is short, or the block reads as a table")
}
