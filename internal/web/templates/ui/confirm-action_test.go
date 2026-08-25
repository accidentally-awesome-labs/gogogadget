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
