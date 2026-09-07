package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// progressbar and meter are announced differently. A progress bar describes a
// task advancing towards completion; a meter describes a measurement inside a
// range. Using meter for a running task tells the user a quota is filling up.
func TestMeterUsesMeterSemantics(t *testing.T) {
	quota := renderComponent(t, Meter(MeterOpts{Percent: 63, Label: "Storage"}))
	assert.Contains(t, quota, `role="meter"`,
		"a quota bar with no role at all is invisible to assistive technology")
	assert.NotContains(t, quota, `role="progressbar"`,
		"calling a quota a progress bar implies it will finish")
	assert.Contains(t, quota, `aria-valuenow="63"`)
	assert.Contains(t, quota, `aria-label="Storage"`)

	// Over-quota clamps rather than overflowing the track.
	assert.Contains(t, renderComponent(t, Meter(MeterOpts{Percent: 140, Label: "x"})),
		`aria-valuenow="100"`)
}
