package ui

import (
	"context"
	"strings"
	"testing"
)

// The component's own contract test. It renders the generated templ output, so
// running it in the derivative proves the whole chain: the manifest installed
// both files, templ generated the renderer, and the root carries the registry
// marker the gallery-coverage check matches on.
func TestExampleCalloutRendersRegistryMarkerAndToneClass(t *testing.T) {
	var out strings.Builder
	err := ExampleCallout(ExampleCalloutOpts{
		Title: "Draining", Body: "Two workers left.", Tone: ExampleToneUrgent,
		Attrs: Attrs{TestID: "example-callout"},
	}).Render(context.Background(), &out)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	html := out.String()
	for _, want := range []string{
		`data-ui="example-callout"`,
		"example-tone-urgent",
		`data-testid="example-callout"`,
		"Two workers left.",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("rendered callout is missing %q:\n%s", want, html)
		}
	}

	var empty strings.Builder
	if err := ExampleCallout(ExampleCalloutOpts{Body: "No title."}).Render(context.Background(), &empty); err != nil {
		t.Fatalf("render(no title): %v", err)
	}
	if strings.Contains(empty.String(), "example-callout-title") {
		t.Fatalf("an empty Title still rendered a heading:\n%s", empty.String())
	}
}
