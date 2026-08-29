package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// and the announcement has to reach a screen reader too.
func TestCopyButtonConfirmsAndKeepsSecretsOutOfExpressions(t *testing.T) {
	html := renderComponent(t, CopyButton(CopyButtonOpts{
		Value: "ggg_sk_live_value", Label: "Copy", CopiedLabel: "Copied",
	}))
	assert.Contains(t, html, `data-copy="ggg_sk_live_value"`)
	assert.Contains(t, html, `x-data="copy"`)
	assert.Contains(t, html, `aria-live="polite"`)
	assert.NotContains(t, html, `x-text="ggg_sk_live_value"`)
	assert.NotContains(t, html, `@click="copy(&#39;`,
		"the value must never be spliced into an Alpine expression")
}

// VisuallyHidden must stay in the accessibility tree: display:none and hidden
