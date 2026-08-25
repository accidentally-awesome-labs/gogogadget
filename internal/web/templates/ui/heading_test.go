package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// together forces a choice between a correct outline and a correct design.
func TestHeadingLevelIsIndependentOfSize(t *testing.T) {
	html := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 3, Size: SizeLG}))
	assert.Contains(t, html, "<h3")
	assert.Contains(t, html, "display-title")

	small := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 1, Size: SizeXS}))
	assert.Contains(t, small, "<h1")
	assert.Contains(t, small, "text-sm")

	// An out-of-range level must not mint a second h1.
	assert.Contains(t, renderComponent(t, Heading(HeadingOpts{Text: "X", Level: 9})), "<h2")
	assert.Contains(t, renderComponent(t, Heading(HeadingOpts{Text: "X"})), "<h2")
}

// A rule with no name is decoration and must stay out of the accessibility
