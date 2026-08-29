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
	// The clamp is a component class plus the one value a caller varies. It
	// used to be an arbitrary Tailwind utility assembled at render time, which
	// the class scanner never saw, so every multi-line Truncate shipped with no
	// clamp at all.
	clamped := renderComponent(t, Truncate(TruncateOpts{Text: full, Lines: 2}))
	assert.Contains(t, clamped, "truncate-lines")
	assert.Contains(t, clamped, "--truncate-lines:2")
}

// A copy button whose label never changes gives no evidence the copy happened,
