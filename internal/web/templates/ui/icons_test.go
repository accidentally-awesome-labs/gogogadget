package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
)

// renderComponent renders a templ component to a string for assertions.
func renderComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// TestIconRegistryIsComplete walks the registry so adding a const without the
// switch arm fails here.
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

// TestIconRegistryHasNoDuplicates guards against a name appearing twice in
// IconNames.
func TestIconRegistryHasNoDuplicates(t *testing.T) {
	seen := make(map[IconName]bool, len(IconNames))
	for _, name := range IconNames {
		if seen[name] {
			t.Errorf("icon %q appears more than once in IconNames", name)
		}
		seen[name] = true
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
