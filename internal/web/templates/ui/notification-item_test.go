package ui

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
)

// The slot exists so a notification row can carry its own controls. It is
// omitted when nil rather than rendered empty: an empty cell with ml-auto steals
// the row's remaining width and pushes the timestamp off a phone screen.
func TestNotificationActionsSlotIsOmittedWhenAbsent(t *testing.T) {
	// The stub stands in for whatever control a caller supplies, and carries a
	// marker so the assertion tells "the slot rendered" from "nothing rendered".
	markRead := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<button data-testid="notification-read">Mark read</button>`)
		return err
	})
	withActions := renderComponent(t, NotificationItem(NotificationItemOpts{
		Title: "Export ready", Unread: true, Actions: markRead,
	}))
	assert.Contains(t, withActions, `data-testid="notification-read"`,
		"the Actions slot must render the caller's controls")

	without := renderComponent(t, NotificationItem(NotificationItemOpts{Title: "Export ready"}))
	assert.NotContains(t, without, "shrink-0 ml-auto",
		"a row with no actions must not carry the empty actions wrapper")
}

// Communication components never take user-controlled raw markup. A single
// unsanitized comment is a stored XSS on every page that renders it, so the
// plain-string path escapes and the slot puts the decision at the call site.
func TestNotificationItemEscapesUserText(t *testing.T) {
	payload := `<img src=x onerror="alert(1)">`

	notification := renderComponent(t, NotificationItem(NotificationItemOpts{Title: payload}))
	assert.NotContains(t, notification, "<img src=x")
}
