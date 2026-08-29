package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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

	assert.Contains(t, renderComponent(t, ToggleButton(ToggleButtonOpts{Label: "Grid", On: true})),
		`aria-pressed="true"`)
}

// Heading level is document structure and size is visual weight. Tying them
