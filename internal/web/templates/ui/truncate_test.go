package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// unreachable and let a screen reader read the clipped text as the whole value.
func TestTruncateKeepsTheFullValue(t *testing.T) {
	full := "a-very-long-project-identifier-that-will-not-fit"
	html := renderComponent(t, Truncate(TruncateOpts{Text: full}))
	assert.Contains(t, html, full, "the full value stays in the DOM")
	assert.Contains(t, html, `title="`+full+`"`)
	assert.Contains(t, html, "truncate")

	clamped := renderComponent(t, Truncate(TruncateOpts{Text: full, Lines: 2}))
	assert.Contains(t, clamped, "-webkit-line-clamp:2")
}

// A copy button whose label never changes gives no evidence the copy happened,
