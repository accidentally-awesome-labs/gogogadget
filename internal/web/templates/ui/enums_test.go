package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every closed enum must answer an invalid value with a defined default rather
// than passing it through. A passed-through value reaches a class name, and a
// component missing its modifier class is a silent visual break in production.
//
// The defaults are asserted individually because each one is a judgement: the
// inert or quietest option wins, so a typo can never promote a control's
// importance, dress a harmless action as destructive, or submit a form.
func TestEveryEnumNormalizesInvalidValues(t *testing.T) {
	assert.Equal(t, SizeMD, Size("").Value())
	assert.Equal(t, SizeMD, Size("medium").Value())
	assert.Equal(t, SizeMD, Size("MD").Value())

	assert.Equal(t, EmphasisSubtle, Emphasis("").Value())
	assert.Equal(t, EmphasisSubtle, Emphasis("filled").Value())

	assert.Equal(t, ActionGhost, Action("").Value())
	assert.Equal(t, ActionGhost, Action("destructive").Value())

	assert.Equal(t, TypeButton, ButtonType("").Value(),
		"an unset button type must be inert: HTML defaults to submit, which submits the form")
	assert.Equal(t, TypeButton, ButtonType("SUBMIT").Value())

	assert.Equal(t, DensityComfortable, Density("").Value())
	assert.Equal(t, OrientationHorizontal, Orientation("").Value())
	assert.Equal(t, AlignStart, Align("").Value())
	assert.Equal(t, PlacementBottom, Placement("").Value())
	assert.Equal(t, KindNeutral, NormalizeKind(""))

	// SortNone and BreakpointNone are meaningful empties, not missing values.
	assert.Equal(t, SortNone, SortDirection("").Value())
	assert.Equal(t, SortNone, SortDirection("ascending").Value())
	assert.Equal(t, BreakpointNone, Breakpoint("").Value())
	assert.Equal(t, BreakpointNone, Breakpoint("xl").Value())
}

// A declared value must survive normalization untouched, and Valid must agree
// with the declared set. Case and whitespace are deliberately not repaired:
// "Danger" and "danger " are typos, and accepting them would make the closed
// set a suggestion.
func TestDeclaredEnumValuesRoundTrip(t *testing.T) {
	for _, v := range Sizes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Emphases {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Actions {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range ButtonTypes {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	for _, v := range Kinds {
		assert.Equal(t, v, NormalizeKind(v))
		assert.True(t, v.Valid())
	}
	for _, v := range append([]Density{}, Densities...) {
		assert.True(t, v.Valid())
	}
	for _, v := range Orientations {
		assert.True(t, v.Valid())
	}
	for _, v := range Aligns {
		assert.True(t, v.Valid())
	}
	for _, v := range Placements {
		assert.True(t, v.Valid())
	}

	assert.False(t, Size("md ").Valid(), "trailing space is a typo, not a value")
	assert.False(t, Kind("Danger").Valid(), "capitalisation is a typo, not a value")
}

// The declared sets must have no duplicates and no accidental empties, since a
// duplicate would make Valid pass for two spellings of one value.
func TestEnumSetsAreWellFormed(t *testing.T) {
	assertDistinct(t, "Sizes", Sizes)
	assertDistinct(t, "Emphases", Emphases)
	assertDistinct(t, "Actions", Actions)
	assertDistinct(t, "ButtonTypes", ButtonTypes)
	assertDistinct(t, "Kinds", Kinds)
	assertDistinct(t, "Densities", Densities)
	assertDistinct(t, "Orientations", Orientations)
	assertDistinct(t, "Aligns", Aligns)
	assertDistinct(t, "Placements", Placements)
	assertDistinct(t, "SortDirections", SortDirections)
	assertDistinct(t, "Breakpoints", Breakpoints)
}

func assertDistinct[T ~string](t *testing.T, name string, values []T) {
	t.Helper()
	seen := map[T]bool{}
	for _, v := range values {
		assert.False(t, seen[v], "%s contains %q twice", name, string(v))
		seen[v] = true
	}
	assert.NotEmpty(t, values, "%s is empty", name)
}
