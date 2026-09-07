package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// progressbar and meter are announced differently. A progress bar describes a
// task advancing towards completion; a meter describes a measurement inside a
// range. Using meter for a running task tells the user a quota is filling up.
func TestProgressBarUsesProgressbarSemantics(t *testing.T) {
	progress := renderComponent(t, ProgressBar(ProgressBarOpts{Value: 63, Max: 100, Label: "Storage"}))
	assert.Contains(t, progress, `role="progressbar"`)
	assert.NotContains(t, progress, "<meter")
}

// A negative value means indeterminate: work is happening but its extent is
// unknown. That is a real state, and it is not the same as zero progress -
// which is why aria-valuenow must be absent rather than 0.
func TestIndeterminateProgressOmitsItsValue(t *testing.T) {
	indeterminate := renderComponent(t, ProgressBar(ProgressBarOpts{Value: -1, Label: "Importing"}))
	assert.Contains(t, indeterminate, `role="progressbar"`)
	assert.NotContains(t, indeterminate, "aria-valuenow")
	assert.Contains(t, indeterminate, "animate-pulse")

	zero := renderComponent(t, ProgressBar(ProgressBarOpts{Value: 0, Max: 100, Label: "Importing"}))
	assert.Contains(t, zero, `aria-valuenow="0"`,
		"zero progress is a known value and must be reported")
}

// A bare percentage tells a screen-reader user nothing about what is at 63%,
// and work measured in files should be announced in files.
func TestProgressReportsWhatIsProgressing(t *testing.T) {
	html := renderComponent(t, ProgressBar(ProgressBarOpts{
		Value: 3, Max: 8, Label: "Files uploaded", ValueText: "3 of 8 files",
	}))
	assert.Contains(t, html, `aria-label="Files uploaded"`)
	assert.Contains(t, html, `aria-valuetext="3 of 8 files"`)
	assert.Contains(t, html, `aria-valuemax="8"`)

	// An unset max is 100, not zero: dividing by a zero max would be a panic
	// or an infinity in the width calculation.
	assert.Contains(t, renderComponent(t, ProgressBar(ProgressBarOpts{Value: 50, Label: "x"})),
		`aria-valuemax="100"`)

	// Out-of-range values clamp rather than overflowing the track.
	assert.Contains(t, renderComponent(t, ProgressBar(ProgressBarOpts{Value: 500, Max: 100, Label: "x"})),
		"width: 100.00%")
}
