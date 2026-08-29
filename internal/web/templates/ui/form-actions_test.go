package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// the documented default and the rendered one disagree.
func TestFormActionsDefaultToEnd(t *testing.T) {
	assert.Contains(t, renderComponent(t, FormActions(FormActionsOpts{})), "justify-end")
	assert.Contains(t, renderComponent(t, FormActions(FormActionsOpts{Align: AlignStart})), "justify-start")
	assert.Contains(t, renderComponent(t, FormActions(FormActionsOpts{Align: AlignCenter})), "justify-center")
}

// A currency symbol or domain suffix is a visual format hint; the field's label
