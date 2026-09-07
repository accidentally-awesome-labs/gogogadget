package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A modal must always be dismissible without JavaScript. Both alert-dialog
// buttons submit the enclosing form method="dialog", which is what closes the
// dialog and records the choice - they were once type="button" with no handler
// at all, trapping the user inside a destructive confirmation.
func TestAlertDialogIsDismissibleWithoutJavaScript(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{
		ID: "confirm-delete", Title: "Delete project?", Message: "This cannot be undone.",
		ConfirmLabel: "Delete", CancelLabel: "Keep it", Kind: KindDanger,
	}))

	require.Contains(t, html, `<form method="dialog"`,
		"without a dialog-method form neither button can close the modal")
	assert.Equal(t, 2, strings.Count(html, `type="submit"`),
		"both choices must submit the form: a type=button with no handler is inert")
	assert.Contains(t, html, `value="cancel"`)
	assert.Contains(t, html, `value="confirm"`)
	assert.NotContains(t, html, `type="button"`,
		"a button that closes a modal must not depend on a script being loaded")

	// The consequence, not just the title, has to be announced.
	assert.Contains(t, html, `role="alertdialog"`)
	assert.Contains(t, html, `aria-labelledby="confirm-delete-title"`)
	assert.Contains(t, html, `aria-describedby="confirm-delete-message"`)
	assert.Contains(t, html, `id="confirm-delete-message"`)
}

// An unset kind must still produce a real colour class. The title used to
// compose "text-{kind}-text" at render time, which for an unset kind was
// "text--text" - a class matching nothing - and for four of the six kinds named
// a utility Tailwind never emitted. It now reads --ui-text from the kind matrix
// the dialog root carries, so one rule covers every kind.
func TestAlertDialogNormalizesItsKind(t *testing.T) {
	html := renderComponent(t, AlertDialog(AlertDialogOpts{ID: "d", Title: "T", Message: "M"}))
	assert.NotContains(t, html, "text--text")
	assert.Contains(t, html, "k-neutral")
	assert.Contains(t, html, "dialog-title")
}
