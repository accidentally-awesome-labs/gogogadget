package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A coloured dot alone encodes meaning in hue only: invisible to a screen
// reader, and identical for a red-green colour-blind reader.
func TestStatusDotAlwaysCarriesText(t *testing.T) {
	html := renderComponent(t, StatusDot(StatusDotOpts{Kind: KindDanger, Label: "Failing"}))
	assert.Contains(t, html, "Failing")
	assert.Contains(t, html, `aria-hidden="true"`, "the dot itself is decoration")
	assert.Contains(t, html, "bg-danger")

	// An unrecognised kind must still colour the dot, not leave it class-less.
	assert.Contains(t, renderComponent(t, StatusDot(StatusDotOpts{Kind: Kind("teal"), Label: "x"})),
		"bg-neutral")
}
