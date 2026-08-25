package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// allows negatives silently rejects valid input.
func TestNumberInputOmitsUnsetBounds(t *testing.T) {
	bare := renderComponent(t, NumberInput(NumberInputOpts{Name: "delta"}))
	assert.NotContains(t, bare, "min=")
	assert.NotContains(t, bare, "max=")
	assert.NotContains(t, bare, "step=")

	bounded := renderComponent(t, NumberInput(NumberInputOpts{Name: "qty", Min: "1", Max: "99", Step: "1"}))
	assert.Contains(t, bounded, `min="1"`)
	assert.Contains(t, bounded, `max="99"`)
}

// A group of checkboxes needs a fieldset and legend, or nothing associates the
