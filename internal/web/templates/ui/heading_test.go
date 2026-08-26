package ui

import (
	"bytes"
	"context"
	"github.com/a-h/templ"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

// together forces a choice between a correct outline and a correct design.
func TestHeadingLevelIsIndependentOfSize(t *testing.T) {
	html := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 3, Size: SizeLG}))
	assert.Contains(t, html, "<h3")
	assert.Contains(t, html, "display-title")

	small := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 1, Size: SizeXS}))
	assert.Contains(t, small, "<h1")
	assert.Contains(t, small, "text-sm")

	// An out-of-range level must not mint a second h1.
	assert.Contains(t, renderComponent(t, Heading(HeadingOpts{Text: "X", Level: 9})), "<h2")
	assert.Contains(t, renderComponent(t, Heading(HeadingOpts{Text: "X"})), "<h2")
}

// A component that owns its own typography needs the tag without the size. If
// Unstyled were ignored, every such component would silently take on a size
// class it never asked for - which is a visual change made to fix a structural
// one, and the kind of regression a refactor slips in unnoticed.
func TestUnstyledHeadingCarriesOnlyTheCallersClass(t *testing.T) {
	sizeClasses := []string{"page-title", "section-title", "display-title", "text-sm"}

	unstyled := renderComponent(t, Heading(HeadingOpts{
		Text: "No projects", Level: 2, Unstyled: true,
		Attrs: Attrs{Class: "font-semibold"},
	}))
	assert.Contains(t, unstyled, "<h2")
	assert.Contains(t, unstyled, `class="font-semibold"`)
	for _, class := range sizeClasses {
		assert.NotContains(t, unstyled, class,
			"an unstyled heading must contribute no size class, but it emitted %q", class)
	}

	// Unstyled does not mean unsized everywhere: a styled heading still carries
	// the size its caller chose.
	styled := renderComponent(t, Heading(HeadingOpts{Text: "Billing", Level: 2, Size: SizeSM}))
	assert.Contains(t, styled, "section-title")
}

// A rule with no name is decoration and must stay out of the accessibility

// A heading is often one sentence with one emphasized clause - a brand-coloured
// second half, an italicised term. A text-only primitive forces that to flatten
// to one tone, and the usual escape is a hand-rolled <h1>, which takes the
// structural decision away from the component that exists to own it.
func TestHeadingRendersCallerMarkupWhenTextIsAbsent(t *testing.T) {
	accent := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `Ship <span class="text-brand-text">faster</span>`)
		return err
	})

	html := renderWithChildren(t, Heading(HeadingOpts{Level: 1, Size: SizeLG}), accent)
	assert.Contains(t, html, "<h1", "the level must still come from the component")
	assert.Contains(t, html, `<span class="text-brand-text">faster</span>`,
		"caller markup was dropped, so an emphasized clause is inexpressible")
}

// Text wins when both are supplied. Rendering both would emit two competing
// bodies in one heading, and the caller would have no way to see which won.
func TestHeadingTextWinsOverChildren(t *testing.T) {
	ignored := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, "SHOULD-NOT-APPEAR")
		return err
	})

	html := renderWithChildren(t, Heading(HeadingOpts{Text: "Declared", Level: 2}), ignored)
	assert.Contains(t, html, "Declared")
	assert.NotContains(t, html, "SHOULD-NOT-APPEAR")
}

// renderWithChildren renders a component with a caller-supplied body. templ
// passes children through the context rather than as an argument, so a slot can
// only be exercised this way.
func renderWithChildren(t *testing.T, c templ.Component, children templ.Component) string {
	t.Helper()
	var buf bytes.Buffer
	if err := c.Render(templ.WithChildren(context.Background(), children), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}
