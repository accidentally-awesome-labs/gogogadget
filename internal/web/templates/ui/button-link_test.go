package ui

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
)

// the catalog middle-clickable and every button keyboard-activatable with Space.
func TestOnlyButtonLinkCarriesADestination(t *testing.T) {
	for _, sample := range []any{ButtonOpts{}, IconButtonOpts{}, ToggleButtonOpts{}} {
		typ := typeOf(sample)
		_, hasHref := typ.FieldByName("Href")
		assert.False(t, hasHref, "%s must not accept Href: it acts, it does not navigate", typ.Name())
	}
	link := renderComponent(t, ButtonLink(ButtonLinkOpts{Label: "Docs", Href: "/docs"}))
	assert.Contains(t, link, `<a href="/docs"`)
	assert.NotContains(t, link, "type=", "a link has no button type")
}

// A new tab that can reach window.opener is a tabnabbing vector, and a jump the

// user did not ask for should be visible before the click.
func TestExternalLinksAreSafeAndSignposted(t *testing.T) {
	html := renderComponent(t, ButtonLink(ButtonLinkOpts{
		Label: "Status", Href: "https://status.example.com", External: true,
	}))
	assert.Contains(t, html, `target="_blank"`)
	assert.Contains(t, html, `rel="noopener noreferrer"`)
	assert.Contains(t, html, `data-ui="icon"`, "an external jump needs a visible marker")

	internal := renderComponent(t, ButtonLink(ButtonLinkOpts{Label: "Docs", Href: "/docs"}))
	assert.NotContains(t, internal, "target=")
	assert.NotContains(t, internal, "rel=")
}

// An icon-only button with no accessible name is announced as "button" and
// nothing else. The name is required, and a caller who forgets gets something

func typeOf(v any) reflect.Type { return reflect.TypeOf(v) }
