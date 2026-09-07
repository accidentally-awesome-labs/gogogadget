package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
