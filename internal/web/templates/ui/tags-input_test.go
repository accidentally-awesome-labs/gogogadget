package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestTagsInputSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, TagsInput(TagsInputOpts{Name: "tags", Value: "go"}))
	assert.Contains(t, html, "<input", "tags must be a real native control")
	assert.Contains(t, html, `name="tags"`, "tags must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
