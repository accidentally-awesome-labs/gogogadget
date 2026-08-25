package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A transcript must be polite, never assertive: assertive interrupts the screen
// reader on every token of a streaming response, which makes the page unusable.
// role="log" is the specific one for an append-only transcript - it tells
// assistive technology that only additions matter, so earlier messages are not
// re-read on each update.
func TestChatLogIsAPoliteAppendOnlyRegion(t *testing.T) {
	html := renderComponent(t, ChatLog(ChatLogOpts{Label: "Assistant conversation"}))
	assert.Contains(t, html, `role="log"`)
	assert.Contains(t, html, `aria-live="polite"`)
	assert.NotContains(t, html, `aria-live="assertive"`)
	assert.Contains(t, html, `aria-label="Assistant conversation"`)

	// Streaming is explicit, so the log can say a response is still arriving.
	assert.Contains(t, renderComponent(t, ChatLog(ChatLogOpts{Label: "x", Streaming: true})), `aria-busy="true"`)
	assert.NotContains(t, html, "aria-busy",
		`aria-busy="false" on every idle log is noise`)
}

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
func TestCommunicationComponentsEscapeUserText(t *testing.T) {
	payload := `<img src=x onerror="alert(1)">`

	comment := renderComponent(t, Comment(CommentOpts{Author: "Ada", Body: payload}))
	assert.NotContains(t, comment, "<img src=x")
	assert.Contains(t, comment, "&lt;img")

	message := renderComponent(t, ChatMessage(ChatMessageOpts{RoleLabel: "You", Body: payload}))
	assert.NotContains(t, message, "<img src=x")

	notification := renderComponent(t, NotificationItem(NotificationItemOpts{Title: payload}))
	assert.NotContains(t, notification, "<img src=x")

	mention := renderComponent(t, MentionChip(MentionChipOpts{Name: payload}))
	assert.NotContains(t, mention, "<img src=x")

	// The escape hatch is a slot, so a caller that has sanitized its own
	// Markdown renders it deliberately and visibly.
	slot := renderComponent(t, Comment(CommentOpts{
		Author: "Ada", Body: "ignored", BodySlot: Badge(BadgeOpts{Text: "rendered"}),
	}))
	assert.Contains(t, slot, "rendered")
	assert.NotContains(t, slot, "ignored")
}

// The @ marker is decoration: a screen reader reading "at Ada Lovelace" inside
// a sentence is confusing, while the glyph is what distinguishes a mention for
// sighted readers.
func TestMentionSigilIsDecorative(t *testing.T) {
	html := renderComponent(t, MentionChip(MentionChipOpts{Name: "ada", Href: "/u/ada"}))
	assert.Contains(t, html, `aria-hidden="true"`)
	assert.Contains(t, html, "ada")
	assert.Contains(t, html, `href="/u/ada"`)
}

// Actor, action and target are separate fields because word order differs by
// language: a caller that pre-joins them into a sentence cannot be translated.
func TestActivityItemKeepsItsPartsSeparate(t *testing.T) {
	typ := typeOf(ActivityItemOpts{})
	for _, field := range []string{"Actor", "Action", "Target"} {
		_, ok := typ.FieldByName(field)
		assert.True(t, ok, "ActivityItemOpts must expose %s separately", field)
	}
	_, joined := typ.FieldByName("Sentence")
	assert.False(t, joined, "a pre-joined sentence cannot be translated")
}

// Relative timestamps need a machine-readable instant too, or the value is
// ambiguous to anything that is not a human reading English.
func TestTimestampsCarryAMachineValue(t *testing.T) {
	html := renderComponent(t, ActivityItem(ActivityItemOpts{
		Actor: "Ada", Action: "invited", Target: "grace",
		Timestamp: "2 hours ago", MachineTime: "2026-01-15T09:30:00Z",
	}))
	assert.Contains(t, html, `<time class="text-xs text-fg-muted" datetime="2026-01-15T09:30:00Z">`)
	assert.Contains(t, html, "2 hours ago")
}

// A checklist that shrinks as you complete it hides what the product can do,
// and a user who wants to revisit a step has nowhere to go.
func TestOnboardingChecklistKeepsCompletedSteps(t *testing.T) {
	html := renderComponent(t, OnboardingChecklist(OnboardingChecklistOpts{
		Title: "Finish setup", ProgressLabel: "Onboarding progress",
		Steps: []ChecklistStep{
			{Label: "Create an organization", Done: true},
			{Label: "Invite a teammate", Href: "/invite"},
			{Label: "Connect billing"},
		},
	}))
	assert.Contains(t, html, "Create an organization", "a completed step stays visible")
	assert.Equal(t, 3, strings.Count(html, "<li"))
	assert.Contains(t, html, `aria-valuenow="1"`)
	assert.Contains(t, html, `aria-valuemax="3"`)
	assert.Contains(t, html, "1 of 3 complete")
	assert.Contains(t, html, "done", "completion is stated in words, not only a tick")
}

// SaaS compositions stay presentation-only: display strings and slots, never a
// billing or identity type, so a module can reuse them without pulling a
// provider package.
func TestProductPatternsTakeDisplayValuesOnly(t *testing.T) {
	for _, sample := range []any{UsageCardOpts{}, MemberItemOpts{}, SettingsSectionOpts{}} {
		typ := typeOf(sample)
		for i := range typ.NumField() {
			field := typ.Field(i)
			pkg := field.Type.PkgPath()
			assert.NotContains(t, pkg, "internal/billing",
				"%s.%s pulls a provider package into the leaf ui layer", typ.Name(), field.Name)
			assert.NotContains(t, pkg, "internal/identity",
				"%s.%s pulls a provider package into the leaf ui layer", typ.Name(), field.Name)
			assert.NotContains(t, pkg, "internal/db/sqlc",
				"%s.%s pulls a database type into the leaf ui layer", typ.Name(), field.Name)
		}
	}
}

// An unaccepted invitation is a different state from membership and needs a
// different action, so it is marked rather than rendered as a member.
func TestMemberItemMarksPendingInvitations(t *testing.T) {
	pending := renderComponent(t, MemberItem(MemberItemOpts{
		Name: "Grace", Email: "grace@example.com", RoleLabel: "Member", Pending: true,
	}))
	assert.Contains(t, pending, "pending")
	assert.Contains(t, pending, "badge-warn")

	member := renderComponent(t, MemberItem(MemberItemOpts{
		Name: "Ada", Email: "ada@example.com", RoleLabel: "Owner",
	}))
	assert.NotContains(t, member, "pending")
}
