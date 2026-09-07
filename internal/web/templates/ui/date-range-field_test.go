package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// entire claim "progressively enhanced" makes.
func TestDateRangeFieldSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, DateRangeField(DateRangeFieldOpts{StartName: "from", EndName: "to"}))
	assert.Contains(t, html, `type="date"`, "date range must be a real native control")
	assert.Contains(t, html, `name="from"`, "date range must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
