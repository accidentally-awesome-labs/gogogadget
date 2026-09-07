package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A caller who names no target wants plain link navigation. Emitting
// hx-target="" makes htmx intercept the click and swap into an empty selector,
// so the request lands nowhere and the href that would have worked is skipped.
func TestUntargetedCursorPaginationEmitsNoHTMX(t *testing.T) {
	const name = "cursor pagination"
	html := renderComponent(t, CursorPagination(CursorPaginationOpts{
		NextURL: "/x?after=1", Label: "Pages",
	}))

	assert.NotContains(t, html, `hx-target=""`, "%s emits an empty target", name)
	assert.NotContains(t, html, "hx-get", "%s should navigate, not swap", name)
	// The link controls keep their href - that is the whole point of dropping
	// the swap.
	assert.Contains(t, html, "href=", "%s must still navigate", name)
}
