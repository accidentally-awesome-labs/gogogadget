package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The last crumb is where the user already is, so linking it invites a
// pointless navigation - and the separators are decoration, because a screen
// reader announces list position already.
func TestBreadcrumbsMarkTheCurrentPage(t *testing.T) {
	html := renderComponent(t, Breadcrumbs(BreadcrumbsOpts{Crumbs: []Crumb{
		{Label: "Projects", Href: "/app/projects"},
		{Label: "Apollo", Href: "/app/projects/apollo"},
		{Label: "Settings", Href: "/app/projects/apollo/settings"},
	}}))

	assert.Equal(t, 2, strings.Count(html, "<a "),
		"the current page must not be a link even when a href is supplied")
	assert.Contains(t, html, `aria-current="page"`)
	assert.Contains(t, html, "<ol", "the order is the meaning")
	assert.Contains(t, html, `aria-label="Breadcrumb"`)
	assert.Contains(t, html, `aria-hidden="true"`, "separators are decoration")
}
