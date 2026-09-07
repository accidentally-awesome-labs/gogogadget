package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A panel that only opens on hover is unreachable by keyboard and absent on
// touch, where there is no hover state at all. The trigger is focusable and the
// behaviour is bound to focus as well as hover.
func TestHoverCardIsReachableWithoutAPointer(t *testing.T) {
	html := renderComponent(t, HoverCard(HoverCardOpts{ID: "preview", Label: "Ada Lovelace"}))
	// A real <button>, not a span carrying tabindex="0". This assertion used to
	// demand the tabindex, which pinned the weaker shape: a span with a tabindex
	// and no role is a tab stop that announces as plain text, so the keyboard
	// user lands on something with no indication it does anything. A button is
	// focusable by construction, so a tabindex on it would be redundant markup.
	assert.Contains(t, html, "<button", "the trigger must be a real control")
	assert.NotContains(t, html, `tabindex="0"`,
		"a button is already focusable; a tabindex here only invites the span version back")
	assert.Contains(t, html, `x-data="uiHoverCard"`)

	// The preview describes the trigger; it does not replace its name. A
	// paragraph of preview text as the accessible name makes the link
	// unreadable in a screen reader's link list.
	assert.Contains(t, html, `aria-describedby="preview"`)
	assert.NotContains(t, html, "aria-labelledby")
	assert.Contains(t, html, `id="preview"`)
	assert.Contains(t, html, "Ada Lovelace")
}
