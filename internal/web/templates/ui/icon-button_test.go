package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// An icon-only button with no accessible name is announced as "button" and
// nothing else. The name is required, and a caller who forgets gets something
// noticeable rather than silence.
func TestIconButtonAlwaysHasAnAccessibleName(t *testing.T) {
	named := renderComponent(t, IconButton(IconButtonOpts{Icon: IconBell, Label: "Notifications"}))
	assert.Contains(t, named, `aria-label="Notifications"`)
	assert.Contains(t, named, `aria-hidden="true"`, "the glyph is decorative; the label names the control")
	assert.Equal(t, 1, strings.Count(named, "Notifications"),
		"the name must be announced once, not duplicated as text and label")

	unnamed := renderComponent(t, IconButton(IconButtonOpts{Icon: IconBell}))
	assert.Contains(t, unnamed, "unlabelled bell button",
		"a missing label must be visible to whoever wrote the call, not silently empty")
}

// aria-pressed states that a control is a toggle. Claiming it on a plain button
// tells the user something toggles when nothing does.
func TestOnlyTogglesReportPressedState(t *testing.T) {
	assert.NotContains(t, renderComponent(t, IconButton(IconButtonOpts{Icon: IconBell, Label: "Bell"})),
		"aria-pressed")

	on := true
	assert.Contains(t, renderComponent(t, IconButton(IconButtonOpts{Icon: IconBell, Label: "Bell", Pressed: &on})),
		`aria-pressed="true"`)
	off := false
	assert.Contains(t, renderComponent(t, IconButton(IconButtonOpts{Icon: IconBell, Label: "Bell", Pressed: &off})),
		`aria-pressed="false"`)
}

// An unregistered icon name renders nothing, and iconSwitch used to return a
// nil templ.Component - so one misspelled name anywhere took down the whole
// page render. A component that passes a caller-supplied name straight through
// is where that surfaced, so the surrounding markup must survive an icon that
// renders nothing at all.
func TestIconButtonSurvivesAnUnregisteredIcon(t *testing.T) {
	button := renderComponent(t, IconButton(IconButtonOpts{Label: "Delete"}))
	assert.Contains(t, button, `aria-label="Delete"`)
}
