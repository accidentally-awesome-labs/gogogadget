package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// An empty list is a resting state, not news. ErrorState is the announced
// counterpart for a region that failed after the user acted.
func TestErrorStateIsAnnounced(t *testing.T) {
	failed := renderComponent(t, ErrorState(ErrorStateOpts{
		Title: "Could not load", Body: "The request failed.", RetryURL: "/retry", Target: "#t",
	}))
	assert.Contains(t, failed, `role="alert"`)
	assert.Contains(t, failed, `href="/retry"`, "the retry works with scripts disabled")
	assert.Contains(t, failed, `hx-get="/retry"`)
	assert.Contains(t, failed, "Try again")

	// A failure with no next step leaves the user only able to reload and hope.
	assert.NotContains(t, renderComponent(t, ErrorState(ErrorStateOpts{Title: "x", Body: "y"})), "<a ")
}
