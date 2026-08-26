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

// A component that owns its own typography needs the tag without the size. If
// Unstyled were ignored, every such component would silently take on a size
// class it never asked for - which is a visual change made to fix a structural
// one, and the kind of regression a refactor slips in unnoticed.
func TestUnstyledHeadingCarriesOnlyTheCallersClass(t *testing.T) {
	sizeClasses := []string{"page-title", "section-title", "display-title", "text-sm"}

	unstyled := renderComponent(t, Heading(HeadingOpts{
		Text: "No projects", Level: 2, Unstyled: true,
		Attrs: Attrs{Class: "font-semibold"},
	}))
	assert.Contains(t, unstyled, "<h2")
	assert.Contains(t, unstyled, `class="font-semibold"`)
	for _, class := range sizeClasses {
		assert.NotContains(t, unstyled, class,
			"an unstyled heading must contribute no size class, but it emitted %q", class)
	}

	// Unstyled does not mean unsized everywhere: a styled heading still carries
	// the size its caller chose.
	styled := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 2, Size: SizeSM}))
	assert.Contains(t, styled, "section-title")
}

// A rule with no name is decoration and must stay out of the accessibility
