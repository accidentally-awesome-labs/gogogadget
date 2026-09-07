package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Disclosure is native details: no script, and the content is present for
// in-page search and print.
func TestDisclosureIsNativeDetails(t *testing.T) {
	html := renderComponent(t, Disclosure(DisclosureOpts{Summary: "More"}))
	assert.Contains(t, html, "<details")
	assert.Contains(t, html, "<summary")
	assert.NotContains(t, html, "x-data")
	assert.NotContains(t, html, "aria-expanded", "the platform announces details state itself")

	assert.Contains(t, renderComponent(t, Disclosure(DisclosureOpts{Summary: "More", Open: true})), "open")
}
