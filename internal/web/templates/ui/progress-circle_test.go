package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The ring is decoration: the semantics live on the wrapper, so assistive
// technology gets a value rather than a description of two circles.
func TestProgressCircleHidesItsGeometry(t *testing.T) {
	html := renderComponent(t, ProgressCircle(ProgressCircleOpts{Value: 25, Max: 100, Label: "Quota"}))
	assert.Contains(t, html, `role="progressbar"`)
	assert.Contains(t, html, `aria-valuenow="25"`)
	assert.Contains(t, html, `aria-hidden="true"`)
	assert.Contains(t, html, `stroke-dashoffset="75.40"`,
		"a quarter complete leaves three quarters of the circumference as gap")
}
