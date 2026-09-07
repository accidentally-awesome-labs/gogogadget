package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestSlugInputSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, SlugInput(SlugInputOpts{Name: "slug", From: "title"}))
	assert.Contains(t, html, "<input", "slug must be a real native control")
	assert.Contains(t, html, `name="slug"`, "slug must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
