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
func TestEmptyStateIsSilent(t *testing.T) {
	assert.NotContains(t, renderComponent(t, EmptyState(EmptyStateOpts{Body: "None"})), "role=")
}

// EmptyVariant is a closed enum this module declares, so its normalization and
// its declared set are exercised here rather than in ui-core's enums_test.go:
// naming it there put a symbol ggg/component/empty-state owns in the payload
// every closure installs.
//
// The default is a judgement: an unset empty state is the standalone card,
// because inline would render without the border its container expects to
// supply.
func TestDeclaredEmptyVariantsRoundTrip(t *testing.T) {
	assert.Equal(t, EmptyCard, EmptyVariant("").Value(),
		"an unset empty state is the standalone card: inline would render "+
			"without the border its container expects to supply")
	assert.Equal(t, EmptyCard, EmptyVariant("banner").Value())
	for _, v := range EmptyVariants {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	assertDistinct(t, "EmptyVariants", EmptyVariants)
}
