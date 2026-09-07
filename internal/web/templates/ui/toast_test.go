package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A toast appears after the fact, so an unannounced one never reaches a screen
// reader at all - unlike a notice rendered with the page, whose default is off.
func TestToastAnnouncesPolitelyByDefault(t *testing.T) {
	assert.Contains(t, renderComponent(t, Toast(ToastOpts{Text: "Saved"})), `role="status"`)
	assert.Contains(t, renderComponent(t, Toast(ToastOpts{Text: "Failed", Live: LiveAssertive})), `role="alert"`)

	// A dismissible toast needs a named control, or the close button is an
	// unlabelled icon.
	dismissible := renderComponent(t, Toast(ToastOpts{Text: "Saved", Dismissible: true}))
	assert.Contains(t, dismissible, `aria-label="Dismiss"`)
	assert.Contains(t, renderComponent(t, Toast(ToastOpts{Text: "x", Dismissible: true, CloseLabel: "Cerrar"})),
		`aria-label="Cerrar"`)
}
