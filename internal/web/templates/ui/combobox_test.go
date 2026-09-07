package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestComboboxSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, Combobox(ComboboxOpts{Name: "region", Options: []Option{{Value: "eu", Label: "eu"}}}))
	assert.Contains(t, html, "<input", "combobox must be a real native control")
	assert.Contains(t, html, `name="region"`, "combobox must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}

// A filterable select is a datalist, not a hand-built listbox: the browser

// supplies filtering, the popup, keyboard navigation and mobile behaviour.
func TestComboboxUsesNativeDatalist(t *testing.T) {
	html := renderComponent(t, Combobox(ComboboxOpts{
		Name: "region", Options: []Option{{Value: "us-east-1", Label: "us-east-1"}},
	}))
	assert.Contains(t, html, `list="region-options"`)
	assert.Contains(t, html, `<datalist id="region-options">`)
	assert.Contains(t, html, `<option value="us-east-1">`)
	assert.NotContains(t, html, `role="listbox"`,
		"listbox promises a keyboard contract this control does not implement")
	assert.NotContains(t, html, `role="combobox"`)
}
