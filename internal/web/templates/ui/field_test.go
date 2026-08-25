package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A required field must say so. FieldOpts.Required was accepted and then
// ignored, so a caller marking a field required got no marker, no aria-required
// and no signal of any kind - the option looked honoured and did nothing.
func TestRequiredFieldIsMarked(t *testing.T) {
	html := renderComponent(t, Field(FieldOpts{Name: "email", Label: "Email", Required: true}))
	assert.Contains(t, html, `aria-hidden="true"`,
		"the visual asterisk is decoration; aria-required on the control carries the meaning")
	assert.Contains(t, html, "*")
	assert.NotContains(t, html, "(optional)")

	optional := renderComponent(t, Field(FieldOpts{Name: "bio", Label: "Bio", Optional: true}))
	assert.Contains(t, optional, "optional")
	assert.NotContains(t, optional, "*")

	// Required must not steal focus. A form whose first required field grabs
	// focus scrolls the page on load and fights a screen reader's own cursor.
	assert.NotContains(t, html, "autofocus")
}

// aria-describedby must name an element that exists. Pointing at "<name>-hint"
// when no hint is rendered leaves a dangling reference: some assistive
// technology announces nothing, and a test asserting the description finds an
// empty string.
func TestFieldDescriptionReferencesOnlyRenderedElements(t *testing.T) {
	bare := renderComponent(t, TextInput(TextInputOpts{Name: "email"}))
	assert.NotContains(t, bare, "aria-describedby",
		"no hint and no error means there is nothing to describe the field")

	withHint := renderComponent(t, Field(FieldOpts{Name: "email", Label: "Email", Hint: "Work address"}))
	require.Contains(t, withHint, `id="email-hint"`)

	withError := renderComponent(t, Field(FieldOpts{Name: "email", Label: "Email", Hint: "Work address", Error: "Required"}))
	assert.Contains(t, withError, `id="email-error"`)
	assert.NotContains(t, withError, `id="email-hint"`,
		"the error replaces the hint, so the hint element must not linger")
}

// The exported helpers exist so a custom control can wire the same semantics as
// Field. They must agree with what Field renders, or a hand-built control gets
// subtly different behaviour.
func TestFieldHelpersMatchWhatFieldRenders(t *testing.T) {
	input, hint, errID := FieldIDs("email")
	assert.Equal(t, "email", input)
	assert.Equal(t, "email-hint", hint)
	assert.Equal(t, "email-error", errID)

	described := FieldARIA(FieldOpts{Name: "email", Hint: "Work address"})
	assert.Equal(t, "email-hint", described["aria-describedby"])
	assert.NotContains(t, described, "aria-invalid")

	invalid := FieldARIA(FieldOpts{Name: "email", Hint: "Work address", Error: "Required", Required: true})
	assert.Equal(t, "email-error", invalid["aria-describedby"],
		"the error describes the field once it exists")
	assert.Equal(t, "true", invalid["aria-invalid"])
	assert.Equal(t, "true", invalid["aria-required"])

	none := FieldARIA(FieldOpts{Name: "email"})
	assert.NotContains(t, none, "aria-describedby",
		"a field with nothing describing it must not reference a missing element")
}

// Server validation is authoritative, so a form must not let the browser block
// submission before the server ever sees it - that is how a 422 fragment, and
// the errors it carries, become unreachable.
func TestFormsDeferValidationToTheServer(t *testing.T) {
	html := renderComponent(t, Form(FormOpts{Method: "post", Action: "/save"}))
	assert.Contains(t, html, "novalidate")
	assert.Equal(t, 1, strings.Count(html, "novalidate"))
}
