package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fails to fill or overwrites a saved password.
func TestPasswordInputDeclaresItsCredential(t *testing.T) {
	assert.Contains(t, renderComponent(t, PasswordInput(PasswordInputOpts{Name: "password"})),
		`autocomplete="current-password"`)
	assert.Contains(t, renderComponent(t, PasswordInput(PasswordInputOpts{Name: "password", Autocomplete: "new-password"})),
		`autocomplete="new-password"`)
}

// An unset numeric bound must render no attribute: min="0" on a field that
