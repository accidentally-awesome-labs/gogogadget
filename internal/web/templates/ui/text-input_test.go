package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// aria-describedby must name an element that exists. Pointing at "<name>-hint"
// when no hint is rendered leaves a dangling reference: some assistive
// technology announces nothing, and a test asserting the description finds an
// empty string.
func TestBareTextInputDescribesNothing(t *testing.T) {
	bare := renderComponent(t, TextInput(TextInputOpts{Name: "email"}))
	assert.NotContains(t, bare, "aria-describedby",
		"no hint and no error means there is nothing to describe the field")
}
