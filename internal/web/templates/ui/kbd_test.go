package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Keys in a chord must read as separate keys, and the joining "+" is decoration.
func TestKbdSeparatesKeysWithoutAnnouncingGlue(t *testing.T) {
	html := renderComponent(t, Kbd(KbdOpts{Keys: []string{"Ctrl", "K"}}))
	assert.Equal(t, 2, strings.Count(html, "<kbd"))
	assert.Contains(t, html, `aria-hidden="true"`)
	require.Contains(t, html, "Ctrl")
	require.Contains(t, html, "K")
}

// typeOf is a local alias so the destination test reads without a reflect
// import at every call site.
