package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A tile with no destination must stay a card: a click handler on a div is
// unreachable by keyboard and announced as nothing.
func TestTileOnlyBecomesALinkWithADestination(t *testing.T) {
	link := renderComponent(t, Tile(TileOpts{Title: "Docs", Href: "/docs", Icon: IconFile}))
	assert.Contains(t, link, `<a href="/docs"`)
	assert.Contains(t, link, `hx-boost="true"`)

	card := renderComponent(t, Tile(TileOpts{Title: "Plain", Body: "No destination"}))
	assert.NotContains(t, card, "<a ")
	assert.NotContains(t, card, "onclick")
}
