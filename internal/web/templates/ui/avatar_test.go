package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// loads and the initials when it does not.
func TestAvatarIsAlwaysNamed(t *testing.T) {
	withImage := renderComponent(t, Avatar(AvatarOpts{Name: "Ada Lovelace", Src: "/a.png"}))
	assert.Contains(t, withImage, `alt="Ada Lovelace"`)
	assert.Contains(t, withImage, `loading="lazy"`)

	fallback := renderComponent(t, Avatar(AvatarOpts{Name: "Ada Lovelace"}))
	assert.Contains(t, fallback, ">AL<", "initials come from the first and last word")
	assert.Contains(t, fallback, `aria-label="Ada Lovelace"`)
	assert.Contains(t, fallback, `role="img"`)

	assert.Contains(t, renderComponent(t, Avatar(AvatarOpts{Name: "Ada de Lovelace"})), ">AL<")
	assert.Contains(t, renderComponent(t, Avatar(AvatarOpts{Name: "Ada"})), ">A<")
	assert.Contains(t, renderComponent(t, Avatar(AvatarOpts{})), ">?<")
}

// The group states the real total once; the overflow chip is decoration, so
// counting it again would tell a screen reader user there are more people than
