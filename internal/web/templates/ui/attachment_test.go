package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A screen reader's link list shows link text out of context, so forty links
// all reading "download" are indistinguishable.
func TestAttachmentLinksTheFileName(t *testing.T) {
	html := renderComponent(t, Attachment(AttachmentOpts{
		Name: "architecture.pdf", Size: "184 KB", Href: "/files/1",
	}))
	assert.Contains(t, html, `<a href="/files/1" class="link truncate">architecture.pdf</a>`)
	assert.NotContains(t, html, ">Download<")
	assert.Contains(t, html, "184 KB")
}
