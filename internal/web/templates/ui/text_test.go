package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// textClass mapped BOTH SizeSM and SizeMD to text-sm. Two things were wrong at
// once: text-base had no typed representation, so base-size body copy could not
// be expressed at all, and one of four declared enum values was silently
// redundant - a caller could switch between them and see no change.
//
// One class per rung, and no two rungs sharing one. The distinctness assertion
// is the one that would have caught the original collision.
func TestTextSizeLadderHasOneClassPerRung(t *testing.T) {
	want := map[Size]string{
		SizeXS: "text-xs",
		SizeSM: "text-sm",
		SizeMD: "text-base",
		SizeLG: "text-lg",
	}

	seen := map[string]Size{}
	for _, size := range Sizes {
		got := textClass(TextOpts{Size: size})
		assert.Equalf(t, want[size], got, "Size %q maps to the wrong class", size)

		other, collides := seen[got]
		assert.Falsef(t, collides,
			"Size %q and Size %q both render %q, so one of the two enum values is unreachable", size, other, got)
		seen[got] = size
	}
}

// MD is the normalize default, so an unsized caller now renders text-base. That
// was a deliberate cutover: every call site that relied on the default was
// pinned to SizeSM first, so nothing moved from 14px to 16px.
func TestUnsizedTextNormalizesToTheBaseRung(t *testing.T) {
	assert.Equal(t, "text-base", textClass(TextOpts{}))
	assert.Equal(t, "text-base text-fg-muted", textClass(TextOpts{Muted: true}))
}
