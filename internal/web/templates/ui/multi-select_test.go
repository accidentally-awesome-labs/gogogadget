package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestMultiSelectSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, MultiSelect(MultiSelectOpts{Name: "envs", Options: []Option{{Value: "dev", Label: "dev"}}}))
	assert.Contains(t, html, "<select", "multi-select must be a real native control")
	assert.Contains(t, html, `name="envs"`, "multi-select must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
