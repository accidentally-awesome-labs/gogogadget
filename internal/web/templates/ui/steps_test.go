package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Position must be stated in text, not left to colour and a filled circle:
// those are invisible to a screen reader and ambiguous to anyone who cannot
// distinguish the two shades.
func TestStepsStateThePositionInText(t *testing.T) {
	html := renderComponent(t, Steps(StepsOpts{Current: 2, Steps: []Step{
		{Label: "Account", Done: true, Href: "/one"},
		{Label: "Organization"},
		{Label: "Billing"},
	}}))

	assert.Contains(t, html, "Step 2 of 3")
	assert.Contains(t, html, "sr-only")
	assert.Contains(t, html, `aria-current="step"`)
	assert.Contains(t, html, `href="/one"`, "a completed step is revisitable")
	assert.NotContains(t, html, `href="/three"`,
		"a future step must not be reachable: its prerequisites are unmet")
}
