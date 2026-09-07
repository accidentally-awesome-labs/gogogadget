package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Communication components never take user-controlled raw markup. A single
// unsanitized comment is a stored XSS on every page that renders it, so the
// plain-string path escapes and the slot puts the decision at the call site.
func TestMentionChipEscapesUserText(t *testing.T) {
	payload := `<img src=x onerror="alert(1)">`

	mention := renderComponent(t, MentionChip(MentionChipOpts{Name: payload}))
	assert.NotContains(t, mention, "<img src=x")
}

// The @ marker is decoration: a screen reader reading "at Ada Lovelace" inside
// a sentence is confusing, while the glyph is what distinguishes a mention for
// sighted readers.
func TestMentionSigilIsDecorative(t *testing.T) {
	html := renderComponent(t, MentionChip(MentionChipOpts{Name: "ada", Href: "/u/ada"}))
	assert.Contains(t, html, `aria-hidden="true"`)
	assert.Contains(t, html, "ada")
	assert.Contains(t, html, `href="/u/ada"`)
}
