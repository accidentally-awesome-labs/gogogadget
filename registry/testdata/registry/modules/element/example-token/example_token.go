package ui

// This file is the payload of the example element module that lives in
// registry/testdata. It is not part of the shipped catalog: no production index
// lists it, so `ggg add` in this repository cannot select it. It exists so
// `ggg registry validate` can install a genuinely third-party leaf element into
// a throwaway derivative, compile it, remove it again, and prove the tree comes
// back byte for byte.
//
// A leaf element imports nothing but templ and the standard library, which is
// the layering rule the ui package exists to enforce. This one imports neither,
// because a closed enum plus its normalizer is the smallest thing that is still
// a real public interface rather than a placeholder.

// ExampleTone is the closed severity vocabulary the example callout renders in.
// It is a full enum rather than a bare string for the same reason every other
// enum here is: an unrecognised value must resolve to something a reader can
// see, not to an empty class attribute.
type ExampleTone string

const (
	ExampleToneCalm   ExampleTone = "calm"
	ExampleToneUrgent ExampleTone = "urgent"
)

// ExampleTones is every declared tone, in the order a reference would present
// them: the resting state first.
var ExampleTones = []ExampleTone{ExampleToneCalm, ExampleToneUrgent}

// Value normalizes an unset or unrecognised tone to calm. Urgent is deliberately
// not the fallback: a typo must not be able to escalate a notice.
func (t ExampleTone) Value() ExampleTone { return normalize(t, ExampleTones, ExampleToneCalm) }

// Valid reports whether t is one of the declared tones.
func (t ExampleTone) Valid() bool { return contains(t, ExampleTones) }

// ExampleToneClass is the component class one tone contributes. The class is
// defined by this module's own CSS fragment, so installing the module installs
// both halves of the contract and removing it takes both away.
func ExampleToneClass(t ExampleTone) string {
	return "example-tone-" + string(t.Value())
}
