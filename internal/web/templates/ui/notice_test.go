package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A notice rendered as part of the page has nothing to announce; marking it
// live makes a screen reader read it again on every unrelated update.
func TestNoticeIsSilentUnlessAsked(t *testing.T) {
	assert.NotContains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindInfo})), "role=")
	assert.Contains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindDanger, Live: LiveAssertive})), `role="alert"`)
	assert.Contains(t, renderComponent(t, Notice(NoticeOpts{Kind: KindSuccess, Live: LivePolite})), `role="status"`)
}
