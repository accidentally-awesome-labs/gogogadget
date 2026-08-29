package ui

import "testing"

// A module's own contract test travels with it. `ggg registry validate` runs
// this in the derivative after installing the closure, so "it compiles" is
// backed by the module's own assertion rather than only by the linker.
func TestExampleToneNormalizesUnknownToCalm(t *testing.T) {
	for _, value := range []ExampleTone{"", "URGENT", "loud"} {
		if got := value.Value(); got != ExampleToneCalm {
			t.Fatalf("ExampleTone(%q).Value() = %q, want %q", value, got, ExampleToneCalm)
		}
		if value.Valid() {
			t.Fatalf("ExampleTone(%q).Valid() = true, want false", value)
		}
	}
	if got := ExampleToneUrgent.Value(); got != ExampleToneUrgent {
		t.Fatalf("ExampleToneUrgent.Value() = %q, want %q", got, ExampleToneUrgent)
	}
	if got := ExampleToneClass("nonsense"); got != "example-tone-calm" {
		t.Fatalf("ExampleToneClass(nonsense) = %q, want the normalized class", got)
	}
}
