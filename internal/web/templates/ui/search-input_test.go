package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A search field with no form around it cannot be submitted at all: Enter does
// nothing and there is no control to press. That was the shipped state, and the
// component's own comments told callers to supply the form - which no caller
// did. The form is part of the component now, so it cannot be forgotten.
func TestSearchInputSubmitsWithoutScript(t *testing.T) {
	html := renderComponent(t, SearchInput(SearchInputOpts{
		Name: "q", AriaLabel: "Search projects", GetURL: "/app/projects",
		Target: "#table-container", IndicatorID: "projects-search-indicator",
	}))

	assert.Contains(t, html, `<form`)
	assert.Contains(t, html, `method="get"`)
	assert.Contains(t, html, `action="/app/projects"`)
	assert.Contains(t, html, `type="submit"`)
	// The submitter is named by the label the caller already translated, so a
	// Spanish page does not get one English word in its toolbar.
	assert.Contains(t, html, "Search projects")
}

// The endpoint's own parameters have to survive the submit. A native GET form
// REPLACES the query string rather than adding to it, so /admin/content?kind=post
// would otherwise search the whole corpus and answer with a list the user was
// not looking at.
func TestSearchInputKeepsEndpointParametersAsFields(t *testing.T) {
	html := renderComponent(t, SearchInput(SearchInputOpts{
		Name: "q", AriaLabel: "Search content", GetURL: "/admin/content?kind=post",
		Target: "#table-container", IndicatorID: "content-search-indicator",
	}))

	assert.Contains(t, html, `action="/admin/content"`)
	assert.Contains(t, html, `<input type="hidden" name="kind" value="post">`)
	// The visible field owns the search parameter. A hidden twin would send the
	// previous query alongside the new one.
	assert.NotContains(t, html, `<input type="hidden" name="q"`)
	// htmx still requests the URL as given, so the scripted path is unchanged.
	assert.Contains(t, html, `hx-get="/admin/content?kind=post"`)
}

// "#" is not an empty selector, it is an invalid one, and querySelector throws
// on it - taking the search request down with it. A caller who named no
// indicator must lose the spinner, not the search.
func TestSearchInputOmitsAnUnnamedIndicator(t *testing.T) {
	html := renderComponent(t, SearchInput(SearchInputOpts{
		Name: "q", AriaLabel: "Search rows", GetURL: "/rows", Target: "#grid",
	}))

	assert.NotContains(t, html, `hx-indicator`)
	assert.NotContains(t, html, `id=""`)
}

// Both the field and the form issue the same request, and Enter triggers both
// (the input's `search` event and the form's submit). They share one hx-sync
// queue on the form so the pair collapses to a single request instead of racing.
func TestSearchInputSynchronisesFieldAndSubmit(t *testing.T) {
	html := renderComponent(t, SearchInput(SearchInputOpts{
		Name: "q", AriaLabel: "Search", GetURL: "/x", Target: "#t",
	}))

	require.Equal(t, 1, strings.Count(html, `hx-sync="this:replace"`),
		"the form owns the queue")
	require.Equal(t, 1, strings.Count(html, `hx-sync="closest form:replace"`),
		"the field joins the form's queue rather than keeping its own")
}
