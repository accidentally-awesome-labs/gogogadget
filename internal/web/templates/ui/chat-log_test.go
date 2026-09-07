package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A transcript must be polite, never assertive: assertive interrupts the screen
// reader on every token of a streaming response, which makes the page unusable.
// role="log" is the specific one for an append-only transcript - it tells
// assistive technology that only additions matter, so earlier messages are not
// re-read on each update.
func TestChatLogIsAPoliteAppendOnlyRegion(t *testing.T) {
	html := renderComponent(t, ChatLog(ChatLogOpts{Label: "Assistant conversation"}))
	assert.Contains(t, html, `role="log"`)
	assert.Contains(t, html, `aria-live="polite"`)
	assert.NotContains(t, html, `aria-live="assertive"`)
	assert.Contains(t, html, `aria-label="Assistant conversation"`)

	// Streaming is explicit, so the log can say a response is still arriving.
	assert.Contains(t, renderComponent(t, ChatLog(ChatLogOpts{Label: "x", Streaming: true})), `aria-busy="true"`)
	assert.NotContains(t, html, "aria-busy",
		`aria-busy="false" on every idle log is noise`)
}
