package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The dropzone must be reachable without a pointer: a drop target that is only
// droppable excludes every keyboard user.
func TestFileDropzoneIsKeyboardReachable(t *testing.T) {
	html := renderComponent(t, FileDropzone(FileDropzoneOpts{Name: "files", Label: "Drop files"}))
	require.Contains(t, html, `<label`)
	assert.Contains(t, html, `for="files"`, "the label must open the picker on click and Enter")
	assert.Contains(t, html, `type="file"`)
	assert.Contains(t, html, "sr-only", "the input is visually hidden, not removed")
	assert.NotContains(t, html, `aria-hidden="true"`+`></input`)
}

// A limit the user cannot exceed must be enforced by the control and the

// A dropzone is frequently not a descendant of the form that submits it: an
// upload zone inside a page whose enclosing form saves something else would
// otherwise send its bytes to that other endpoint. Nested <form> is dropped by
// the HTML parser, so HTML's form attribute is the only no-script answer.
func TestDropzoneNamesAnExternalFormOwner(t *testing.T) {
	html := renderComponent(t, FileDropzone(FileDropzoneOpts{
		Name: "media", Label: "Drop files", Form: "media-upload",
	}))
	assert.Contains(t, html, `form="media-upload"`,
		"the input must name its form owner, or it submits to whatever form encloses it")

	without := renderComponent(t, FileDropzone(FileDropzoneOpts{Name: "media", Label: "Drop files"}))
	assert.NotContains(t, without, "form=",
		`form="" makes the input own no form at all, which is worse than omitting the attribute`)
}

// entire claim "progressively enhanced" makes.
func TestFileDropzoneSubmitsWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, FileDropzone(FileDropzoneOpts{Name: "files", Label: "Drop"}))
	assert.Contains(t, html, `type="file"`, "dropzone must be a real native control")
	assert.Contains(t, html, `name="files"`, "dropzone must submit a named value")
	assert.NotContains(t, html, `type="hidden"`,
		"a widget that keeps its real value in a hidden input loses it when the script fails")
}
