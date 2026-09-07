package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Href belongs to ButtonLink alone: a link navigates and a button acts, which
// is what makes the catalog middle-clickable and every button keyboard-activatable
// with Space.
//
// The other direction - that an acting control never accepts a destination - is
// a contract over every installed renderer, so it is derived in contract_test.go
// rather than asserted here against three options structs three other modules
// own.
func TestOnlyButtonLinkCarriesADestination(t *testing.T) {
	typ := typeOf(ButtonLinkOpts{})
	_, hasHref := typ.FieldByName("Href")
	assert.True(t, hasHref, "ButtonLink is the destination-carrying member of the family")

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
