package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIconRegistryIsComplete walks the registry so adding a const without the
// switch arm fails here.
//
// The registry of names is ui-core's (icons.go); the switch that draws them is
// this module's. So the joint claim - every declared name renders an svg - is
// asserted here, where the renderer lives. Asserting it from ui-core's payload
// named Icon and IconOpts from the one payload every closure installs.
func TestIconRegistryIsComplete(t *testing.T) {
	if len(IconNames) == 0 {
		t.Fatal("IconNames is empty")
	}
	for _, name := range IconNames {
		html := renderComponent(t, Icon(IconOpts{Name: name, Attrs: Attrs{Class: "w-4 h-4"}}))
		if html == "" {
			t.Errorf("icon %q has a const but no switch arm in icons.templ", name)
			continue
		}
		if !bytes.Contains([]byte(html), []byte("<svg")) {
			t.Errorf("icon %q did not render an svg", name)
		}
		if !bytes.Contains([]byte(html), []byte(`class="w-4 h-4"`)) {
			t.Errorf("icon %q must apply the caller's class", name)
		}
	}
}

// An icon is decorative by default: the control beside it already carries the
// name, so announcing the glyph too is noise. An icon that is the *only* thing
// in a control has to be named instead, which is what Label does.
func TestIconIsDecorativeUnlessLabelled(t *testing.T) {
	decorative := renderComponent(t, Icon(IconOpts{Name: IconBell}))
	assert.Equal(t, 1, strings.Count(decorative, `aria-hidden="true"`),
		"a duplicated attribute means the spread value can never override the literal")
	assert.NotContains(t, decorative, `role="img"`)
	assert.NotContains(t, decorative, "aria-label")

	labelled := renderComponent(t, Icon(IconOpts{Name: IconBell, Label: "Notifications"}))
	assert.NotContains(t, labelled, "aria-hidden",
		"a labelled icon that stays aria-hidden cannot be announced at all")
	assert.Contains(t, labelled, `role="img"`)
	assert.Contains(t, labelled, `aria-label="Notifications"`)
}

// An unknown icon name must render nothing, not crash. iconSwitch returned a
// nil templ.Component, and rendering nil panics - so a single misspelled icon
// name anywhere took down the whole page render, including inside components
// that pass a caller-supplied name through (IconButton, MenuItem).
func TestUnknownIconRendersNothingWithoutPanicking(t *testing.T) {
	for _, name := range []IconName{"", "trashcan", "Bell"} {
		html := renderComponent(t, Icon(IconOpts{Name: name}))
		assert.Empty(t, html, "an unregistered name %q must render nothing", name)
	}
}
