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

	// One placeholder class, shared with every other skeleton on the page. The
	// lines used to inline `rounded bg-surface-raised animate-pulse`, which
	// meant they animated straight through a reduced-motion preference that
	// .skeleton already honours.
	lines := renderComponent(t, Skeleton(SkeletonOpts{Lines: 3}))
	assert.Equal(t, 3, strings.Count(lines, `class="skeleton h-4`))
	assert.Contains(t, lines, "w-2/3", "the last line is short, or the block reads as a table")
}
