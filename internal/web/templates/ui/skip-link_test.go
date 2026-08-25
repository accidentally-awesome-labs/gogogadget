package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A skip link must be focusable while invisible: display:none removes it from
// the tab order, which makes it exactly as useful as not having one.
func TestSkipLinkIsFocusableWhileHidden(t *testing.T) {
	html := renderComponent(t, SkipLink(SkipLinkOpts{Label: "Skip to content"}))
	assert.Contains(t, html, "sr-only")
	assert.Contains(t, html, "focus:not-sr-only")
	assert.NotContains(t, html, "display:none")
	assert.NotContains(t, html, ` hidden`)
	assert.Contains(t, html, `href="#content"`, "it defaults to the content landmark")

	assert.Contains(t, renderComponent(t, SkipLink(SkipLinkOpts{Target: "#main", Label: "Skip"})), `href="#main"`)
}
