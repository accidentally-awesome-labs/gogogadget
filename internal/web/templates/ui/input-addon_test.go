package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// already states what it is, so announcing the affix interrupts the reading.
func TestInputAddonIsNotAnnounced(t *testing.T) {
	assert.Contains(t, renderComponent(t, InputAddon(InputAddonOpts{Text: "$"})), `aria-hidden="true"`)
}
