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

// A notice rendered as part of the page has nothing to announce; marking it
// live makes a screen reader read it again on every unrelated update.
func TestNoticeIsSilentUnlessAsked(t *testing.T) {
	assert.NotContains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindInfo})), "role=")
	assert.Contains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindDanger, Live: LiveAssertive})), `role="alert"`)
	assert.Contains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindSuccess, Live: LivePolite})), `role="status"`)
}
