package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestEnhancedControlsSubmitWithoutJavaScript(t *testing.T) {
	cases := []struct {
		name    string
		html    string
		element string
		field   string
	}{
		{"combobox", renderComponent(t, Combobox(ComboboxOpts{Name: "region", Options: []Option{{Value: "eu", Label: "eu"}}})), "<input", `name="region"`},
		{"multi-select", renderComponent(t, MultiSelect(MultiSelectOpts{Name: "envs", Options: []Option{{Value: "dev", Label: "dev"}}})), "<select", `name="envs"`},
		{"tags", renderComponent(t, TagsInput(TagsInputOpts{Name: "tags", Value: "go"})), "<input", `name="tags"`},
		{"slug", renderComponent(t, SlugInput(SlugInputOpts{Name: "slug", From: "title"})), "<input", `name="slug"`},
		{"otp", renderComponent(t, OTPInput(OTPInputOpts{Name: "code"})), "<input", `name="code"`},
		{"dropzone", renderComponent(t, FileDropzone(FileDropzoneOpts{Name: "files", Label: "Drop"})), `type="file"`, `name="files"`},
		{"date range", renderComponent(t, DateRangeField(DateRangeFieldOpts{StartName: "from", EndName: "to"})), `type="date"`, `name="from"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Contains(t, tc.html, tc.element, "%s must be a real native control", tc.name)
			assert.Contains(t, tc.html, tc.field, "%s must submit a named value", tc.name)
			assert.NotContains(t, tc.html, `type="hidden"`,
				"a widget that keeps its real value in a hidden input loses it when the script fails")
		})
	}
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

// One input, not one box per digit: a box-per-digit widget breaks paste, breaks
// SMS and password-manager autofill, and gives a screen reader several
