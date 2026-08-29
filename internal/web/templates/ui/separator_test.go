package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// tree; a named one is a real boundary worth announcing.
func TestSeparatorAnnouncesItselfOnlyWhenNamed(t *testing.T) {
	plain := renderComponent(t, Separator(SeparatorOpts{}))
	assert.Contains(t, plain, `aria-hidden="true"`)
	assert.NotContains(t, plain, "aria-label")

	named := renderComponent(t, Separator(SeparatorOpts{Label: "Danger zone"}))
	assert.Contains(t, named, `aria-label="Danger zone"`)
	assert.NotContains(t, named, "aria-hidden")

	vertical := renderComponent(t, Separator(SeparatorOpts{Orientation: OrientationVertical, Label: "Filters"}))
	assert.Contains(t, vertical, `role="separator"`)
	assert.Contains(t, vertical, `aria-orientation="vertical"`)
}

// The name is what makes an avatar meaningful: it is the alt text when the image
