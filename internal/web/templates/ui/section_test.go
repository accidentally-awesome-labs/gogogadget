package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A <section> without an accessible name is not a landmark - it is an anonymous
// div with extra letters - so the attribute is omitted rather than dangling
// when there is nothing to point it at.
func TestSectionIsALandmarkOnlyWhenNamed(t *testing.T) {
	named := renderComponent(t, Section(SectionOpts{
		Title: "Billing", Level: 3, Attrs: Attrs{ID: "billing"},
	}))
	assert.Contains(t, named, `aria-labelledby="billing-title"`)
	assert.Contains(t, named, `id="billing-title"`)
	assert.Contains(t, named, "<h3")

	// No ID means nothing to reference: a dangling aria-labelledby is worse
	// than none, because some assistive technology announces nothing at all.
	anonymous := renderComponent(t, Section(SectionOpts{Title: "Billing"}))
	assert.NotContains(t, anonymous, "aria-labelledby")
}
