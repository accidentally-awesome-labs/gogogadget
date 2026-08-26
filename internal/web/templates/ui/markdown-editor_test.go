package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The textarea is the form value. Without this, "works with no JavaScript" is a
// claim about a control that never submits.
func TestMarkdownEditorSubmitsANativeTextarea(t *testing.T) {
	html := renderComponent(t, MarkdownEditor(MarkdownEditorOpts{
		Name: "body_md", Value: "## Heading", Rows: 8,
	}))

	assert.Contains(t, html, `name="body_md"`)
	assert.Contains(t, html, "<textarea")
	assert.Contains(t, html, "## Heading")
	// The editor stores Markdown, so nothing here may be a contenteditable
	// surface: contenteditable produces HTML, which the server refuses to trust.
	assert.NotContains(t, html, "contenteditable")
}

// The toolbar's buttons do nothing without the controller. Shipping them visible
// promises formatting that a JavaScript-less page cannot deliver.
func TestEditorToolbarIsHiddenUntilScripted(t *testing.T) {
	html := renderComponent(t, EditorToolbar(EditorToolbarOpts{For: "body_md"}))

	assert.Contains(t, html, "hidden")
	// It is still a real group with a name, so once revealed a screen reader can
	// tell the buttons apart from page navigation.
	assert.Contains(t, html, `role="group"`)
	assert.Contains(t, html, `aria-label="Formatting"`)
}

// A toolbar of icon glyphs reads as nothing. Every control needs a name, and
// the shortcut belongs in the title where a pointer user can discover it.
func TestEveryToolbarControlIsNamed(t *testing.T) {
	html := renderComponent(t, EditorToolbar(EditorToolbarOpts{For: "body_md"}))

	// One aria-label belongs to the group itself; every remaining one must be a
	// button's, so the counts match only after excluding the group's.
	require.Equal(t, strings.Count(html, "<button"),
		strings.Count(html, "aria-label=")-strings.Count(html, `aria-label="Formatting"`),
		"a toolbar button with no accessible name is unusable by screen reader")
	assert.Contains(t, html, `title="Bold (Ctrl+B)"`)
	// Buttons inside a form must be typed, or the first one submits it.
	assert.Equal(t, strings.Count(html, "<button"), strings.Count(html, `type="button"`))
}

// The syntax lives in data attributes so the controller has no per-button logic
// and a new control is a markup change.
func TestToolbarCarriesItsMarkdownSyntax(t *testing.T) {
	html := renderComponent(t, EditorToolbar(EditorToolbarOpts{For: "body_md"}))

	assert.Contains(t, html, `data-editor-action="wrap"`)
	assert.Contains(t, html, `data-editor-prefix="**"`)
	assert.Contains(t, html, `data-editor-suffix="**"`)
	// Line controls carry a prefix and no suffix - that difference is what tells
	// the controller to prepend to lines rather than wrap a selection.
	assert.Contains(t, html, `data-editor-prefix="## "`)
}

// The preview is a live region because it updates while the author types; making
// it assertive would interrupt a screen reader user mid-sentence, on every
// keystroke.
func TestEditorPreviewIsAPoliteLiveRegion(t *testing.T) {
	html := renderComponent(t, EditorPreview(EditorPreviewOpts{ID: "p"}))

	assert.Contains(t, html, `aria-live="polite"`)
	assert.NotContains(t, html, `aria-live="assertive"`)
	assert.Contains(t, html, `role="status"`)
}

// The preview must come from the server. A client-side Markdown renderer would
// be a second implementation with its own escaping rules, and the author would
// be reviewing output that readers never see.
func TestPreviewIsRequestedFromTheServer(t *testing.T) {
	html := renderComponent(t, MarkdownEditor(MarkdownEditorOpts{
		Name: "body_md", PreviewURL: "/admin/content/preview",
	}))

	assert.Contains(t, html, `hx-post="/admin/content/preview"`)
	// Debounced, so the server is not asked to render on every keystroke.
	assert.Contains(t, html, "delay:400ms")
	assert.Contains(t, html, `hx-target="#body_md-preview"`)
}

// No preview URL means no preview region: an empty box labelled "Preview" that
// never fills tells the author the feature is broken.
func TestNoPreviewURLRendersNoPreviewRegion(t *testing.T) {
	html := renderComponent(t, MarkdownEditor(MarkdownEditorOpts{Name: "body_md"}))

	assert.NotContains(t, html, "hx-post")
	assert.NotContains(t, html, `aria-label="Preview"`)
}

// Alt text is filled in at the point where it is known. A prompt shown after
// insertion can be dismissed, and dismissed prompts produce undescribed images.
func TestMediaInsertsAltTextWithTheImage(t *testing.T) {
	html := renderComponent(t, MediaPicker(MediaPickerOpts{
		ID: "m", Items: []MediaItem{{URL: "/u/a.png", Alt: "A wiring diagram", Name: "a.png"}},
	}))

	assert.Contains(t, html, `data-editor-insert="![A wiring diagram](/u/a.png)"`)
}

// Upload is a real file input, so choosing a file never depends on a drag
// gesture that a keyboard user cannot perform.
func TestMediaUploadWorksWithoutDragging(t *testing.T) {
	html := renderComponent(t, MediaPicker(MediaPickerOpts{
		ID: "m", UploadURL: "/admin/media", UploadLabel: "Choose a file",
	}))

	assert.Contains(t, html, `type="file"`)
}

// An empty gallery must say so. A blank area is indistinguishable from a
// component that failed to load.
func TestEmptyMediaPickerExplainsItself(t *testing.T) {
	html := renderComponent(t, MediaPicker(MediaPickerOpts{ID: "m", EmptyLabel: "No uploads yet."}))

	assert.Contains(t, html, "No uploads yet.")
}

// The character count needs the limit the textarea enforces, or the two disagree
// and the author is stopped by a limit the counter never showed.
func TestCharacterLimitIsEnforcedAndCounted(t *testing.T) {
	html := renderComponent(t, MarkdownEditor(MarkdownEditorOpts{Name: "body_md", MaxLength: 500}))

	assert.Contains(t, html, `maxlength="500"`)
	assert.Contains(t, html, "500")
	assert.Contains(t, html, "char-counter")
}

// The media button targets a container the editor renders. Pointing htmx at an
// id that does not exist swaps into nothing, silently.
func TestMediaButtonTargetExists(t *testing.T) {
	html := renderComponent(t, MarkdownEditor(MarkdownEditorOpts{
		Name: "body_md", MediaURL: "/admin/media",
	}))

	assert.Contains(t, html, `hx-target="#body_md-media"`)
	assert.Contains(t, html, `id="body_md-media"`)
}
