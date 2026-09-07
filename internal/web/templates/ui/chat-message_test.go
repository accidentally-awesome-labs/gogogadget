package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Alignment and colour are the only visual cues for who spoke, and both are
// invisible to a screen reader - so the speaker is named in text.
func TestChatMessageNamesItsSpeaker(t *testing.T) {
	user := renderComponent(t, ChatMessage(ChatMessageOpts{Role: ChatRoleUser, RoleLabel: "You", Body: "Hi"}))
	assert.Contains(t, user, "You")
	assert.Contains(t, user, "ml-auto", "the user's message is right-aligned")

	assistant := renderComponent(t, ChatMessage(ChatMessageOpts{Role: ChatRoleAssistant, RoleLabel: "Assistant", Body: "Hello"}))
	assert.NotContains(t, assistant, "ml-auto")

	// A half-written message must say so, or it looks finished.
	streaming := renderComponent(t, ChatMessage(ChatMessageOpts{
		Role: ChatRoleAssistant, RoleLabel: "Assistant", Body: "The deploy shipped", Streaming: true,
	}))
	assert.Contains(t, streaming, "still writing")
}

// Communication components never take user-controlled raw markup. A single
// unsanitized comment is a stored XSS on every page that renders it, so the
// plain-string path escapes and the slot puts the decision at the call site.
func TestChatMessageEscapesUserText(t *testing.T) {
	payload := `<img src=x onerror="alert(1)">`

	message := renderComponent(t, ChatMessage(ChatMessageOpts{RoleLabel: "You", Body: payload}))
	assert.NotContains(t, message, "<img src=x")
}

// ChatRole is a closed enum this module declares, so its normalization and its
// declared set are exercised here rather than in ui-core's enums_test.go:
// naming it there put a symbol ggg/component/chat-message owns in the payload
// every closure installs.
//
// The default is a judgement: an unattributed message is the assistant's,
// because attributing it to the user would put words in their mouth in a
// transcript.
func TestDeclaredChatRolesRoundTrip(t *testing.T) {
	assert.Equal(t, ChatRoleAssistant, ChatRole("").Value(),
		"an unattributed message is the assistant's: attributing it to the user "+
			"would put words in their mouth in a transcript")
	for _, v := range ChatRoles {
		assert.Equal(t, v, v.Value())
		assert.True(t, v.Valid())
	}
	assertDistinct(t, "ChatRoles", ChatRoles)
}
