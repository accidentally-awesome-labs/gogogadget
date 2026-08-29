package ui

import (
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Vals and Headers must reach the attribute map as JSON strings. templ renders
// strings; handed a map it drops the attribute entirely, and a request that
// silently loses its parameters looks like a working control that does nothing.
func TestHXValsAndHeadersEncodeAsJSON(t *testing.T) {
	out := templ.Attributes{}
	applyHX(out, HX{
		Post:    "/move",
		Vals:    map[string]string{"card": "c1", "to": "done"},
		Headers: map[string]string{"X-Reason": "drag"},
	})

	vals, ok := out["hx-vals"].(string)
	require.True(t, ok, "hx-vals must be a string; a map is dropped by the renderer")
	assert.JSONEq(t, `{"card":"c1","to":"done"}`, vals)

	headers, ok := out["hx-headers"].(string)
	require.True(t, ok, "hx-headers must be a string")
	assert.JSONEq(t, `{"X-Reason":"drag"}`, headers)
}

// An empty map must emit nothing rather than "{}": an empty hx-vals is noise on
// every element that happens to declare the field.
func TestEmptyHXMapsEmitNothing(t *testing.T) {
	out := templ.Attributes{}
	applyHX(out, HX{Post: "/move", Vals: map[string]string{}})

	_, hasVals := out["hx-vals"]
	assert.False(t, hasVals)
	_, hasHeaders := out["hx-headers"]
	assert.False(t, hasHeaders)
}
