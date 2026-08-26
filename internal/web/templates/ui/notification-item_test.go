package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The slot exists so a notification row can carry its own controls. It is
// omitted when nil rather than rendered empty: an empty cell with ml-auto steals
// the row's remaining width and pushes the timestamp off a phone screen.
func TestNotificationActionsSlotIsOmittedWhenAbsent(t *testing.T) {
	withActions := renderComponent(t, NotificationItem(NotificationItemOpts{
		Title: "Export ready", Unread: true,
		Actions: Button(ButtonOpts{Label: "Mark read", Attrs: Attrs{TestID: "notification-read"}}),
	}))
	assert.Contains(t, withActions, `data-testid="notification-read"`,
		"the Actions slot must render the caller's controls")

	without := renderComponent(t, NotificationItem(NotificationItemOpts{Title: "Export ready"}))
	assert.NotContains(t, without, "shrink-0 ml-auto",
		"a row with no actions must not carry the empty actions wrapper")
}
