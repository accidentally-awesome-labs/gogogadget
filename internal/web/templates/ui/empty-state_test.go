package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The three presentations are genuinely different, and one variant styled three
// ways by callers is how ad hoc colspan empty rows appear.
func TestEmptyStateVariantsMatchTheirContainer(t *testing.T) {
	inline := renderComponent(t, EmptyState(EmptyStateOpts{Body: "None", Variant: EmptyInline}))
	assert.Contains(t, inline, "table-empty")
	assert.NotContains(t, inline, "card",
		"inline sits inside a TableCard, which already draws the border")

	card := renderComponent(t, EmptyState(EmptyStateOpts{Body: "None"}))
	assert.Contains(t, card, "card", "an unset variant is the standalone card")

	page := renderComponent(t, EmptyState(EmptyStateOpts{Body: "None", Variant: EmptyPage}))
	assert.Contains(t, page, "page-narrow")
}

// "No results for this filter" and "nothing exists yet" are different messages
// needing different actions. Offering "create your first project" to someone
// whose search missed is wrong.
func TestFilteredEmptyStateOffersAnEscape(t *testing.T) {
	filtered := renderComponent(t, EmptyState(EmptyStateOpts{
		Body: "No match", Filtered: true, ClearURL: "/app/projects", Target: "#t",
	}))
	assert.Contains(t, filtered, `href="/app/projects"`)
	assert.Contains(t, filtered, "Clear filters")
	assert.Contains(t, filtered, `hx-push-url="true"`)

	first := renderComponent(t, EmptyState(EmptyStateOpts{Body: "Nothing yet"}))
	assert.NotContains(t, first, "Clear filters")
}

// An empty list is a resting state, not news. ErrorState is the announced
// counterpart for a region that failed after the user acted.
func TestEmptyStateIsSilentAndErrorStateIsNot(t *testing.T) {
	assert.NotContains(t, renderComponent(t, EmptyState(EmptyStateOpts{Body: "None"})), "role=")

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
