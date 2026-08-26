package ui

import (
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every form control hardcoded id={o.Name}. That is correct for a singleton
// form and wrong the moment a control repeats per row: /admin/users renders a
// name="role" select on every user and /admin/flags a name="rollout" input on
// every flag, so the page emitted one identical id dozens of times. `for=` and
// `aria-describedby` both resolve to the FIRST match, so every row's label and
// every row's error described row one. A label pointing at the wrong control is
// worse than no label at all.
//
// One helper resolves the id, and this table proves each control routes through
// it. A control added without an ID field fails the enumeration check below.
func controlIDRenders() map[string]func(id, name string) templ.Component {
	return map[string]func(id, name string) templ.Component{
		"text-input": func(id, name string) templ.Component {
			return TextInput(TextInputOpts{ID: id, Name: name})
		},
		"number-input": func(id, name string) templ.Component {
			return NumberInput(NumberInputOpts{ID: id, Name: name})
		},
		"select": func(id, name string) templ.Component {
			return Select(SelectOpts{ID: id, Name: name, Options: []Option{{Value: "a", Label: "A"}}})
		},
		"multi-select": func(id, name string) templ.Component {
			return MultiSelect(MultiSelectOpts{ID: id, Name: name, Options: []Option{{Value: "a", Label: "A"}}})
		},
		"combobox": func(id, name string) templ.Component {
			return Combobox(ComboboxOpts{ID: id, Name: name, Options: []Option{{Value: "a", Label: "A"}}})
		},
		"color-input": func(id, name string) templ.Component {
			return ColorInput(ColorInputOpts{ID: id, Name: name})
		},
		"date-field": func(id, name string) templ.Component {
			return DateField(DateFieldOpts{ID: id, Name: name})
		},
		"date-time-field": func(id, name string) templ.Component {
			return DateTimeField(DateTimeFieldOpts{ID: id, Name: name})
		},
		"file-input": func(id, name string) templ.Component {
			return FileInput(FileInputOpts{ID: id, Name: name})
		},
		"file-dropzone": func(id, name string) templ.Component {
			return FileDropzone(FileDropzoneOpts{ID: id, Name: name, Label: "Drop files"})
		},
	}
}

func TestEveryControlHonoursAnExplicitID(t *testing.T) {
	for component, render := range controlIDRenders() {
		html := renderComponent(t, render("role-42", "role"))

		assert.Containsf(t, html, `id="role-42"`,
			"%s ignored the explicit ID, so a per-row control still collides with every other row", component)
		assert.NotContainsf(t, html, `id="role"`,
			"%s emitted the name as an id as well; two id attributes on one element is a browser coin flip", component)
		assert.Containsf(t, html, `name="role"`,
			"%s must keep submitting under Name - the handler reads the name, not the id", component)
		assert.Equalf(t, 1, strings.Count(html, ` id="role-42"`),
			"%s emitted the resolved id more than once", component)
	}
}

// Name stays the default, because every existing caller depends on it and a
// singleton form addresses its control by field name.
func TestControlIDDefaultsToName(t *testing.T) {
	for component, render := range controlIDRenders() {
		html := renderComponent(t, render("", "email"))
		assert.Containsf(t, html, `id="email"`,
			"%s stopped defaulting its id to Name, which breaks every existing caller", component)
	}
}

// Attrs.ID is honoured as the fallback for a single-element control, so a caller
// who already set the id there keeps working - and must not suddenly emit two id
// attributes on the same element.
func TestAttrsIDIsHonouredWithoutDuplicating(t *testing.T) {
	html := renderComponent(t, TextInput(TextInputOpts{Name: "role", Attrs: Attrs{ID: "role-7"}}))

	assert.Contains(t, html, `id="role-7"`)
	assert.Equal(t, 1, strings.Count(html, " id="),
		"exactly one id attribute may reach the element")
}

// The point of the whole change: the same control repeated with the same name
// must produce distinct ids, and each row's aria-describedby must name that
// row's own error element.
func TestRepeatedControlsDoNotShareAnID(t *testing.T) {
	var rows []string
	for _, id := range []string{"rollout-alpha", "rollout-beta"} {
		rows = append(rows, renderComponent(t, Field(FieldOpts{
			ID: id, Name: "rollout", Label: "Rollout percent", HiddenLabel: true,
			Error: "Must be 0-100",
		})))
	}
	page := strings.Join(rows, "")

	assert.Contains(t, page, `for="rollout-alpha"`)
	assert.Contains(t, page, `for="rollout-beta"`)
	assert.Contains(t, page, `id="rollout-alpha-error"`)
	assert.Contains(t, page, `id="rollout-beta-error"`)
	assert.NotContains(t, page, `for="rollout"`,
		"a label whose for= names the shared field name points every row at the first row")
}

// Field renders the hint and error elements; FieldARIA names them. Both must
// resolve through the same id, or a control describes an element that does not
// exist - which some assistive technology announces as nothing at all.
func TestFieldMarkupAndARIAAgreeOnTheResolvedID(t *testing.T) {
	opts := FieldOpts{ID: "role-42", Name: "role", Label: "Staff role", Hint: "Applies immediately"}

	require.Equal(t, "role-42", FieldControlID(opts))
	assert.Equal(t, "role-42-hint", FieldARIA(opts)["aria-describedby"])

	html := renderComponent(t, Field(opts))
	assert.Contains(t, html, `for="role-42"`)
	assert.Contains(t, html, `id="role-42-hint"`)

	withError := FieldOpts{ID: "role-42", Name: "role", Label: "Staff role", Hint: "Applies immediately", Error: "Pick one"}
	assert.Equal(t, "role-42-error", FieldARIA(withError)["aria-describedby"])
	assert.Contains(t, renderComponent(t, Field(withError)), `id="role-42-error"`)
}

// A control's own aria-describedby must name the resolved id too, or the row's
// input points at the first row's hint.
func TestControlDescribedByFollowsTheResolvedID(t *testing.T) {
	html := renderComponent(t, NumberInput(NumberInputOpts{
		ID: "rollout-beta", Name: "rollout", Hint: "Percent of accounts",
	}))
	assert.Contains(t, html, `aria-describedby="rollout-beta-hint"`)
}

// FileDropzone's label wraps its input, so the two must agree: a for= naming a
// different id than the input carries is a zone that opens someone else's
// picker. Its hint is rendered rather than merely referenced - referencing an
// element that is never drawn is a dangling aria reference.
func TestDropzoneLabelAndInputAgree(t *testing.T) {
	html := renderComponent(t, FileDropzone(FileDropzoneOpts{
		ID: "upload-2", Name: "file", Label: "Drop files", Hint: "Up to 10 MB",
	}))

	assert.Contains(t, html, `for="upload-2"`)
	assert.Contains(t, html, `id="upload-2"`)
	assert.Contains(t, html, `aria-describedby="upload-2-hint"`)
	assert.Contains(t, html, `id="upload-2-hint"`,
		"the hint element must exist, or aria-describedby names nothing")
}

// A combobox's datalist is addressed by id. Two comboboxes sharing a name would
// share one list element, and the second row's options would silently be the
// first row's.
func TestComboboxListFollowsTheControlID(t *testing.T) {
	html := renderComponent(t, Combobox(ComboboxOpts{
		ID: "owner-2", Name: "owner", Options: []Option{{Value: "a", Label: "A"}},
	}))

	assert.Contains(t, html, `list="owner-2-options"`)
	assert.Contains(t, html, `id="owner-2-options"`)
}
