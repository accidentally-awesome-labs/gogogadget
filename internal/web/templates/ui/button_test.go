package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// otherwise.
func TestButtonIsInertUnlessTyped(t *testing.T) {
	assert.Contains(t, renderComponent(t, Button(ButtonOpts{Label: "Save"})),
		`type="button"`)
	assert.Contains(t, renderComponent(t, Button(ButtonOpts{Label: "Save", Type: TypeSubmit})),
		`type="submit"`)
	assert.Contains(t, renderComponent(t, Button(ButtonOpts{Label: "Reset", Type: TypeReset})),
		`type="reset"`)
	// An unrecognised type must not reach the attribute: the browser would
	// treat it as submit, which is the one behaviour we are preventing.
	assert.Contains(t, renderComponent(t, Button(ButtonOpts{Label: "X", Type: ButtonType("send")})),
		`type="button"`)
}

// Busy is not Disabled. Disabling the control the user just activated moves
// focus to the top of the document, so a keyboard or screen-reader user loses

// their place in the middle of an action.
func TestBusyButtonKeepsFocusAndReportsState(t *testing.T) {
	busy := renderComponent(t, Button(ButtonOpts{Label: "Saving", Busy: true}))
	assert.Contains(t, busy, `aria-busy="true"`)
	assert.NotContains(t, busy, "disabled",
		"a busy control must stay focusable; disabling it throws focus away")

	idle := renderComponent(t, Button(ButtonOpts{Label: "Save"}))
	assert.NotContains(t, idle, "aria-busy",
		`aria-busy="false" and no aria-busy say the same thing; emitting it everywhere is noise`)

	unavailable := renderComponent(t, Button(ButtonOpts{Label: "Save", Disabled: true}))
	assert.Contains(t, unavailable, "disabled")
}

// A control with a destination is a link and a control that acts is a button.
// Exactly one action renderer accepts Href, which is what keeps every anchor in
