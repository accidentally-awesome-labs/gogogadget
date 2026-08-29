package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// server; the counter is only a readout, so it reads the limit from one place.
func TestCharCounterOnlyReportsTheLimit(t *testing.T) {
	html := renderComponent(t, CharCounter(CharCounterOpts{For: "summary", Max: 280, Label: "characters"}))
	assert.Contains(t, html, `data-for="summary"`)
	assert.Contains(t, html, `data-max="280"`)
	assert.Contains(t, html, `aria-live="polite"`,
		"an assertive region would interrupt a screen reader on every keystroke")
	assert.Contains(t, html, "280 characters")
}

// A password field must declare which credential it is, or a manager either
