package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Actor, action and target are separate fields because word order differs by
// language: a caller that pre-joins them into a sentence cannot be translated.
func TestActivityItemKeepsItsPartsSeparate(t *testing.T) {
	typ := typeOf(ActivityItemOpts{})
	for _, field := range []string{"Actor", "Action", "Target"} {
		_, ok := typ.FieldByName(field)
		assert.True(t, ok, "ActivityItemOpts must expose %s separately", field)
	}
	_, joined := typ.FieldByName("Sentence")
	assert.False(t, joined, "a pre-joined sentence cannot be translated")
}

// Relative timestamps need a machine-readable instant too, or the value is
// ambiguous to anything that is not a human reading English.
func TestTimestampsCarryAMachineValue(t *testing.T) {
	html := renderComponent(t, ActivityItem(ActivityItemOpts{
		Actor: "Ada", Action: "invited", Target: "grace",
		Timestamp: "2 hours ago", MachineTime: "2026-01-15T09:30:00Z",
	}))
	assert.Contains(t, html, `<time class="text-xs text-fg-muted" datetime="2026-01-15T09:30:00Z">`)
	assert.Contains(t, html, "2 hours ago")
}
