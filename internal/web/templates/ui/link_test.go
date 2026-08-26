package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A linked heading needs a real anchor for navigation and none of the link's
// typography: brand colour plus an underline on a 2xl bold heading changes how
// it looks in order to fix how it behaves, which are two unrelated decisions.
// Unstyled mirrors HeadingOpts.Unstyled exactly - the caller's classes are the
// whole of it.
func TestUnstyledLinkContributesNoClassOfItsOwn(t *testing.T) {
	styled := renderComponent(t, Link(LinkOpts{Label: "Read", Href: "/blog/1"}))
	assert.Contains(t, styled, `class="link"`,
		"the default link must keep its own typography")

	unstyled := renderComponent(t, Link(LinkOpts{
		Label: "Read", Href: "/blog/1", Unstyled: true,
		Attrs: Attrs{Class: "display-title"},
	}))
	assert.Contains(t, unstyled, `class="display-title"`,
		"an unstyled link renders the caller's classes and nothing else")
	assert.NotContains(t, unstyled, `class="link`,
		"an unstyled link must not force brand colour or an underline")

	bare := renderComponent(t, Link(LinkOpts{Label: "Read", Href: "/blog/1", Unstyled: true}))
	assert.NotContains(t, bare, "class=",
		"with no caller class there is nothing to emit, so no empty class attribute either")
}
