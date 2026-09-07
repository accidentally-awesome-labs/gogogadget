package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// One input, not one box per digit: a box-per-digit widget breaks paste, breaks
// SMS and password-manager autofill, and gives a screen reader several
// unlabelled fields instead of one.
func TestOTPInputIsOneAutofillableField(t *testing.T) {
	html := renderComponent(t, OTPInput(OTPInputOpts{Name: "code", Length: 6}))
	assert.Equal(t, 1, strings.Count(html, "<input"))
	assert.Contains(t, html, `autocomplete="one-time-code"`)
	assert.Contains(t, html, `inputmode="numeric"`)
	assert.Contains(t, html, `maxlength="6"`)

	// A missing length must still produce a usable field.
	assert.Contains(t, renderComponent(t, OTPInput(OTPInputOpts{Name: "code"})), `maxlength="6"`)
}

// entire claim "progressively enhanced" makes.
func TestOTPInputSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, OTPInput(OTPInputOpts{Name: "code"}))
	assert.Contains(t, html, "<input", "otp must be a real native control")
	assert.Contains(t, html, `name="code"`, "otp must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
