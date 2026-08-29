package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Exclusivity comes from the native name attribute, so it survives with
// JavaScript disabled. Two accordions on one page must not share a name, or
// opening a panel in one closes a panel in the other.
func TestAccordionExclusivityIsNative(t *testing.T) {
	exclusive := renderComponent(t, Accordion(AccordionOpts{
		Exclusive: true, Name: "faq",
		Panels: []AccordionPanel{{Summary: "A", Body: "a"}, {Summary: "B", Body: "b"}},
	}))
	assert.Equal(t, 2, strings.Count(exclusive, `name="faq"`))
	assert.NotContains(t, exclusive, "x-data", "exclusivity needs no script")

	independent := renderComponent(t, Accordion(AccordionOpts{
		Panels: []AccordionPanel{{Summary: "A", Body: "a"}, {Summary: "B", Body: "b"}},
	}))
	assert.NotContains(t, independent, "name=",
		"without Exclusive the panels open independently")

	// A missing name still groups, but the fallback is shared - which is why
	// Name matters once a page has two accordions.
	assert.Contains(t, renderComponent(t, Accordion(AccordionOpts{
		Exclusive: true, Panels: []AccordionPanel{{Summary: "A", Body: "a"}},
	})), `name="ui-accordion"`)
}

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
