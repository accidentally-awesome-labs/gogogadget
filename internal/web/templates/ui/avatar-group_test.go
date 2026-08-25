package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// there are.
func TestAvatarGroupCountsPeopleOnce(t *testing.T) {
	people := []AvatarOpts{{Name: "A B"}, {Name: "C D"}, {Name: "E F"}, {Name: "G H"}, {Name: "I J"}}
	html := renderComponent(t, AvatarGroup(AvatarGroupOpts{People: people}))
	assert.Contains(t, html, `aria-label="5 people"`)
	assert.Contains(t, html, "+2")
	assert.Equal(t, 3, strings.Count(html, `data-ui="avatar"`), "Max defaults to three")
	assert.Contains(t, html, `aria-hidden="true"`, "the overflow chip must not be counted twice")

	short := renderComponent(t, AvatarGroup(AvatarGroupOpts{People: people[:2]}))
	assert.NotContains(t, short, "+", "no overflow chip when everyone fits")
}

// Truncation is visual. Cutting the string server-side would make the rest
