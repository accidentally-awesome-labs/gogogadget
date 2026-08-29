package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

// The dropzone must be reachable without a pointer: a drop target that is only
