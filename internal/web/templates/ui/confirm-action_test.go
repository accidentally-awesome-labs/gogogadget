package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ConfirmAction exists to replace hx-confirm, which calls window.confirm: an OS
// dialog whose copy cannot be translated, cannot be styled, cannot be asserted
// on through the DOM, and which on some platforms offers a "prevent further
// dialogs" checkbox that silently disables every later confirmation.
func TestConfirmActionCarriesTranslatableCopy(t *testing.T) {
	html := renderComponent(t, ConfirmAction(ConfirmActionOpts{
		ID: "delete-apollo", TriggerLabel: "Delete project",
		Title: "¿Eliminar el proyecto?", Message: "Esto no se puede deshacer.",
		ConfirmLabel: "Eliminar", CancelLabel: "Conservar", Kind: KindDanger,
		HX: HX{Delete: "/app/projects/apollo", Target: "closest tr", Swap: "outerHTML"},
	}))

	// The copy is real template text, not an attribute value.
	assert.Contains(t, html, "¿Eliminar el proyecto?")
	assert.Contains(t, html, "Esto no se puede deshacer.")
	assert.NotContains(t, html, "hx-confirm",
		"the point of this component is that no hx-confirm is needed")

	// The request rides on the confirm control, so the server contract is
	// unchanged - the dialog only decides whether it is issued.
	assert.Contains(t, html, `hx-delete="/app/projects/apollo"`)
	assert.Contains(t, html, `hx-target="closest tr"`)
	assert.Contains(t, html, `hx-swap="outerHTML"`)

	// A destructive confirmation must look destructive at the trigger too, not
	// only inside the dialog.
	assert.Contains(t, html, "btn-danger")
	assert.Contains(t, html, `role="alertdialog"`)
}

// Cancel comes first in the DOM so the platform's own initial focus lands on
// the safe choice. A destructive dialog that opens with Delete focused turns a
// stray Enter - the key that opened it - into the deletion.
func TestAlertDialogPutsCancelFirst(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{
		ID: "d", Title: "Delete?", Message: "Gone forever.",
		ConfirmLabel: "Delete", CancelLabel: "Keep",
	}))
	cancel := strings.Index(html, "data-ui-dialog-cancel")
	confirm := strings.Index(html, "data-ui-dialog-confirm")
	require.Positive(t, cancel)
	require.Positive(t, confirm)
	assert.Less(t, cancel, confirm, "cancel must precede confirm in the DOM")
}

// Without a request the dialog still works: it records the choice in
// returnValue, which is what a caller wiring its own handler needs.
func TestAlertDialogWithoutRequestStillRecordsTheChoice(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{ID: "d", Title: "T", Message: "M"}))
	assert.Contains(t, html, `value="cancel"`)
	assert.Contains(t, html, `value="confirm"`)
	assert.NotContains(t, html, "hx-delete")
}

// htmx preventDefaults a click on any type="submit" control, so a confirm button
// carrying a request never submits its enclosing form method="dialog": the
// platform never closes the dialog and never records returnValue. The dialog
// stays over the page. It looked fine for a long time only because the gallery's
// fixture swaps its own trigger away, so the response destroyed the dialog
// regardless - a confirmation with hx-swap="none" or an out-of-region target had
// no way to dismiss itself.
func TestConfirmWithRequestClosesItsOwnDialog(t *testing.T) {
	html := renderComponent(t, ConfirmAction(ConfirmActionOpts{
		ID: "danger", TriggerLabel: "Delete", Title: "Delete?", Message: "Gone for good.",
		ConfirmLabel: "Delete", CancelLabel: "Keep",
		HX: HX{Post: "/things", Swap: "none"},
	}))

	confirm := tagContaining(html, "data-ui-dialog-confirm")
	assert.Contains(t, confirm, `x-on:click`,
		"a confirm control carrying a request must close the dialog itself; htmx suppresses the native submit that would have")
	assert.Contains(t, html, `close(&#39;danger&#39;, &#39;confirm&#39;)`,
		"the recorded choice must survive, or returnValue silently becomes empty for confirm while cancel still reports")
	assert.Contains(t, confirm, "hx-post",
		"the request must still be issued - closing the dialog must not disconnect the button")
}

// Without a request the platform's own form method="dialog" submit closes the
// dialog and records the value. Adding a handler there would close it twice.
func TestConfirmWithoutRequestKeepsTheNativePath(t *testing.T) {
	html := renderComponent(t, ConfirmAction(ConfirmActionOpts{
		ID: "plain", TriggerLabel: "Archive", Title: "Archive?", Message: "You can undo this.",
		ConfirmLabel: "Archive", CancelLabel: "Keep",
	}))

	confirm := tagContaining(html, "data-ui-dialog-confirm")
	assert.NotContains(t, confirm, "x-on:click",
		"nothing suppresses the native submit here, so an explicit close would fire on top of the platform's")
	assert.Contains(t, confirm, `value="confirm"`)
}

// Cancel is never the control htmx intercepts, so it must keep working through
// the platform in every case - it is the escape hatch from a modal.
func TestCancelAlwaysUsesTheNativePath(t *testing.T) {
	for name, hx := range map[string]HX{"with request": {Post: "/things"}, "without": {}} {
		html := renderComponent(t, ConfirmAction(ConfirmActionOpts{
			ID: "c", TriggerLabel: "Go", Title: "Sure?", Message: "m",
			ConfirmLabel: "Yes", CancelLabel: "No", HX: hx,
		}))
		cancel := tagContaining(html, "data-ui-dialog-cancel")
		assert.NotContainsf(t, cancel, "hx-", "%s: cancel must never carry the request", name)
		assert.Containsf(t, cancel, `value="cancel"`, "%s", name)
	}
}

// tagContaining returns the whole opening tag that carries marker. Slicing from
// the marker itself drops every attribute rendered before it, which silently
// turns "this attribute is absent" into "this attribute is earlier in the tag".
func tagContaining(html, marker string) string {
	at := strings.Index(html, marker)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(html[:at], "<")
	end := strings.Index(html[at:], ">")
	if start < 0 || end < 0 {
		return ""
	}
	return html[start : at+end+1]
}
