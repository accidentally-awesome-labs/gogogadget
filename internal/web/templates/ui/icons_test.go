package ui

import (
	"bytes"
	"context"
	"testing"

	"github.com/a-h/templ"
)

// renderComponent renders a templ component to a string for assertions. It
// lives with ui-core because every payload in the package uses it and ui-core
// is the one module every closure installs.
func renderComponent(t *testing.T, c templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

// TestIconRegistryHasNoDuplicates guards against a name appearing twice in
// IconNames. The names are ui-core's own list; whether each one draws an svg is
// ggg/element/icon's contract and is asserted in icon_test.go.
func TestIconRegistryHasNoDuplicates(t *testing.T) {
	seen := make(map[IconName]bool, len(IconNames))
	for _, name := range IconNames {
		if seen[name] {
			t.Errorf("icon %q appears more than once in IconNames", name)
		}
		seen[name] = true
	}
}
