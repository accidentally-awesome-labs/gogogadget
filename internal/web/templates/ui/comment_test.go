package ui

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
)

// Communication components never take user-controlled raw markup. A single
// unsanitized comment is a stored XSS on every page that renders it, so the
// plain-string path escapes and the slot puts the decision at the call site.
func TestCommentEscapesUserText(t *testing.T) {
	payload := `<img src=x onerror="alert(1)">`

	comment := renderComponent(t, Comment(CommentOpts{Author: "Ada", Body: payload}))
	assert.NotContains(t, comment, "<img src=x")
	assert.Contains(t, comment, "&lt;img")

	// The escape hatch is a slot, so a caller that has sanitized its own
	// Markdown renders it deliberately and visibly.
	// The stub carries a marker of its own so the assertions below distinguish
	// "the slot rendered" from "nothing rendered at all".
	sanitized := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<span data-testid="comment-body-slot">rendered</span>`)
		return err
	})
	slot := renderComponent(t, Comment(CommentOpts{
		Author: "Ada", Body: "ignored", BodySlot: sanitized,
	}))
	assert.Contains(t, slot, "rendered")
	assert.Contains(t, slot, `data-testid="comment-body-slot"`,
		"the slot content itself must reach the output, not merely displace Body")
	assert.NotContains(t, slot, "ignored")
}
