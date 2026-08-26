package ui

// The composed-component half of the example closure in registry/testdata. It
// depends on element/example-token for its tone vocabulary rather than
// redefining one, which is what makes the two modules a dependency closure
// instead of two unrelated files: installing component/example-callout pulls
// the element in, and the element cannot be removed while the callout is
// installed.

// ExampleCalloutOpts is the options struct the renderer takes. Every exported
// renderer in this package has the shape `templ Name(o NameOpts)` and every
// NameOpts embeds Attrs as the field Attrs, so a caller can set id, class, test
// id, data, Alpine and HTMX without the component surrendering its own
// semantics.
type ExampleCalloutOpts struct {
	Title string
	Body  string
	Tone  ExampleTone
	Attrs Attrs
}

// exampleCalloutClass is the full class list the root carries: the component's
// own base class plus the tone contribution its element dependency owns.
func exampleCalloutClass(tone ExampleTone) string {
	return "example-callout " + ExampleToneClass(tone)
}
