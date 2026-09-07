package ui

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
)

// ValueSlot and Value both fill one slot, so the precedence is stated rather
// than left to whichever field a caller happened to set.
func TestDescriptionValueSlotWinsOverValue(t *testing.T) {
	// The stub stands in for whatever rich content a caller renders, and carries
	// a marker so the assertions tell "the slot won" from "nothing rendered".
	plan := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, `<span data-testid="description-value-slot">Pro</span>`)
		return err
	})

	html := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "Plan", Value: "plain text", ValueSlot: plan},
	}}))
	assert.Contains(t, html, "Pro")
	assert.Contains(t, html, `data-testid="description-value-slot"`,
		"the slot content itself must reach the output, not merely displace Value")
	assert.NotContains(t, html, "plain text")

	// Copyable adds the control only when there is a value to copy.
	copyable := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "ID", Value: "prj_1", Copyable: true},
	}}))
	assert.Contains(t, copyable, `data-copy="prj_1"`)

	slotOnly := renderComponent(t, DescriptionList(DescriptionListOpts{Items: []DescriptionItem{
		{Term: "Plan", ValueSlot: plan, Copyable: true},
	}}))
	assert.NotContains(t, slotOnly, "data-copy",
		"there is no text to place on the clipboard")
}
