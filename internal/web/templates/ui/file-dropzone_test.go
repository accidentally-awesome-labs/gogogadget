package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
