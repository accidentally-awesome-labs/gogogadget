package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MenuItem declared an Attrs field that no menu rendered. Every menu in the
// catalog - ContextMenu, Menubar, RowActions, Kanban - delegates to
// DropdownMenu, so a single dropped merge left every menu item unable to carry a
// data-testid, an id or a title. A caller wrote it, read it back in the struct
// literal, and never learned it was gone; a Playwright contract addressing a
// specific row action had nothing to address.
//
// Both active branches are covered: an item with a destination renders an <a>,
// an item that acts renders a <button>, and they are separate code paths.
func TestMenuItemAttrsReachBothBranches(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		ID: "row-actions", Label: "Actions",
		Items: []MenuItem{
			{Label: "Open", Href: "/projects/1", Attrs: Attrs{
				ID: "open-1", TestID: "row-open", Title: "Open project 1",
			}},
			{Label: "Archive", HX: HX{Post: "/projects/1/archive"}, Attrs: Attrs{
				ID: "archive-1", TestID: "row-archive", Title: "Archive project 1",
			}},
		},
	}))

	for _, want := range []string{
		`id="open-1"`, `data-testid="row-open"`, `title="Open project 1"`,
		`id="archive-1"`, `data-testid="row-archive"`, `title="Archive project 1"`,
	} {
		assert.Containsf(t, html, want,
			"MenuItem.Attrs was set but %s never reached the item element", want)
	}
}

// The item's own class must survive a caller appending one. Two class attributes
// on one tag is what a naive merge produces, and the browser keeps only the
// first - so either the component's colour or the caller's utility silently
// disappears.
func TestMenuItemClassIsAdditiveAndEmittedOnce(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		ID: "menu", Label: "Actions",
		Items: []MenuItem{{Label: "Delete", Kind: KindDanger, HX: HX{Delete: "/x"}, Attrs: Attrs{Class: "mt-1"}}},
	}))

	item := elementContaining(t, html, "Delete")
	assert.Equal(t, 1, strings.Count(item, "class="),
		"the item must carry exactly one class attribute, or one of the two class lists is dropped")
	assert.Contains(t, item, "text-danger-text", "the component's own kind class must survive")
	assert.Contains(t, item, "mt-1", "the caller's class must be appended, not substituted")
	assert.Contains(t, item, "w-full text-left", "the acting branch's own classes must survive too")
}

// A disabled item must not carry the request. aria-disabled is a promise that
// nothing happens; htmx does not read aria-disabled, so an hx-post there would
// fire on click and break the promise.
func TestDisabledMenuItemCarriesNoRequest(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		ID: "menu", Label: "Actions",
		Items: []MenuItem{{Label: "Restore", Disabled: true, HX: HX{Post: "/restore"}}},
	}))

	item := elementContaining(t, html, "Restore")
	assert.NotContains(t, item, "hx-post",
		"a disabled item must not issue the request its aria-disabled state denies")
}

// elementContaining returns the opening tag of the innermost element whose text
// is body, so an assertion about one menu item cannot be satisfied by markup
// belonging to a sibling.
func elementContaining(t *testing.T, html, body string) string {
	t.Helper()

	at := strings.Index(html, ">"+body)
	if at < 0 {
		t.Fatalf("no element renders %q", body)
	}
	open := strings.LastIndex(html[:at], "<")
	if open < 0 {
		t.Fatalf("no opening tag before %q", body)
	}
	return html[open : at+1]
}
