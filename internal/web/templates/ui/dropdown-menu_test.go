package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panelTargetOf returns the id the trigger points its popovertarget at.
func panelTargetOf(t *testing.T, html string) string {
	const attr = `popovertarget="`
	start := strings.Index(html, attr)
	require.Positive(t, start, "the trigger renders no popovertarget, so nothing discloses without script")
	rest := html[start+len(attr):]
	return rest[:strings.Index(rest, `"`)]
}

// The panel id has to be unique on the page AND identical between two renders
// of the same page. A counter satisfies the first and fails the second, which
// is what made every rendered surface byte-unstable and every visual baseline
// unsettleable; TestScenariosAreDeterministic is the far end of this rule and
// this is the near end.
func TestPanelIDIsStableAcrossRenders(t *testing.T) {
	menu := DropdownMenuOpts{Label: "Actions", Items: []MenuItem{
		{Label: "Rename", Href: "/projects/1/rename"},
		{Label: "Delete", Kind: KindDanger, HX: HX{Delete: "/projects/1"}},
	}}

	first := panelTargetOf(t, renderComponent(t, DropdownMenu(menu)))
	second := panelTargetOf(t, renderComponent(t, DropdownMenu(menu)))
	assert.Equal(t, first, second, "an id derived from call order cannot survive a second render")
}

// Two menus on one page must not share a panel id: popovertarget resolves to
// the first element with that id, so a collision means one row's trigger opens
// another row's commands - silently, and worse than the duplicate accessible
// name that usually accompanies it.
//
// The acting case is the one that matters. A navigating menu is distinguished by
// its hrefs, but a row-action menu carries the row's identity in its request,
// so seeding from label and href alone collided for exactly the repetition this
// component exists to support.
func TestPanelIDDistinguishesMenusThatDifferOnlyByRequest(t *testing.T) {
	rowMenu := func(row string) DropdownMenuOpts {
		return DropdownMenuOpts{Label: "Actions", Items: []MenuItem{
			{Label: "Archive", HX: HX{Post: "/projects/" + row + "/archive"}},
			{Label: "Delete", Kind: KindDanger, HX: HX{Delete: "/projects/" + row}},
		}}
	}

	one := panelTargetOf(t, renderComponent(t, DropdownMenu(rowMenu("1"))))
	forty := panelTargetOf(t, renderComponent(t, DropdownMenu(rowMenu("40"))))
	assert.NotEqual(t, one, forty, "two rows' menus share a panel, so one trigger opens the other's commands")
}

// A caller-supplied identity always wins, in preference order. Callers that
// have a row key - Kanban, the team scenario - already pass one, and an
// explicit id is the only thing that can be relied on to be unique when two
// menus genuinely offer identical commands.
func TestCallerSuppliedPanelIDWins(t *testing.T) {
	items := []MenuItem{{Label: "Rename", Href: "/x"}}

	assert.Equal(t, "card-7-menu-panel", panelTargetOf(t, renderComponent(t,
		DropdownMenu(DropdownMenuOpts{ID: "card-7-menu", Label: "Actions", Items: items}))))
	assert.Equal(t, "row-7-panel", panelTargetOf(t, renderComponent(t,
		DropdownMenu(DropdownMenuOpts{Label: "Actions", Items: items, Attrs: Attrs{ID: "row-7"}}))))
	assert.Equal(t, "menu-7-panel", panelTargetOf(t, renderComponent(t,
		DropdownMenu(DropdownMenuOpts{Label: "Actions", Items: items, Attrs: Attrs{TestID: "menu-7"}}))))
}

// The panel is a native popover and the trigger opens it declaratively, which
// is the whole no-script fallback: with the controller absent the platform still
// discloses the commands. A panel that lost either attribute would render as an
// ordinary div - visible at all times, in flow, and never dismissed.
func TestPanelDisclosesWithoutScript(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{{Label: "Rename", Href: "/x"}},
	}))

	assert.Contains(t, html, " popover ", "the panel is not a popover, so nothing hides or discloses it")
	assert.Contains(t, html, `popovertarget="`)
	// Not bound with Alpine: with scripting off the browser derives the expanded
	// state from popovertarget itself, and a server-rendered "false" would be a
	// stale lie the moment the panel opened.
	assert.NotContains(t, html, `:aria-expanded`)
	assert.NotContains(t, html, `x-show`)
}

// A separator is not a command: it carries no label, no href and no handler, so
// rendering it as an item would put an empty, focusable row in the menu.
//
// This test used to spend its last assertion on hx-confirm - on an item with an
// href and no request, where the attribute gates nothing - and asserted nothing
// about the separator beyond the role. What a divider must be is the subject
// here: an <hr> with the separator role, no accessible name, and no tab stop.
func TestMenuSeparatorRendersAsSeparator(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{
			{Label: "Rename", Href: "/x"},
			{Separator: true},
			{Label: "Delete", Href: "/y", Kind: KindDanger},
		},
	}))

	assert.Contains(t, html, `<hr role="separator"`,
		"a divider is an hr: a div with the role is a line assistive technology reads as content")
	assert.Equal(t, 1, strings.Count(html, `role="separator"`),
		"one separator in, one separator out")
	assert.Equal(t, 2, strings.Count(html, "<a "),
		"a separator must not render as a link")
	assert.Equal(t, 2, strings.Count(html, `class="block px-3 py-2 text-sm`),
		"a separator must not take an item's padding, hover or text styling")
	// The two commands survive around it, in order, so a separator between them
	// is a divider rather than a truncation point.
	assert.Less(t, strings.Index(html, "Rename"), strings.Index(html, `role="separator"`))
	assert.Less(t, strings.Index(html, `role="separator"`), strings.Index(html, "Delete"))
}

// hx-confirm gates an htmx request and nothing else. On a plain link or an inert
// button htmx never processes the activation, so the attribute promises a prompt
// that never appears and an action that never runs - which is what the
// Operations scenario shipped. The component refuses the combination.
func TestMenuConfirmOnlyRidesARealRequest(t *testing.T) {
	acting := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{{Label: "Delete", Kind: KindDanger, Confirm: "Delete this?", HX: HX{Delete: "/x"}}},
	}))
	assert.Contains(t, acting, `hx-confirm="Delete this?"`,
		"an item that issues a request keeps its declared confirmation")

	for name, item := range map[string]MenuItem{
		"navigating": {Label: "Delete", Href: "/y", Confirm: "Delete this?"},
		"inert":      {Label: "Cancel", Confirm: "Cancel this?"},
		// Target and swap modify a request some other attribute has to declare.
		"modifiers only": {Label: "Cancel", Confirm: "Cancel this?", HX: HX{Target: "#t", Swap: "outerHTML"}},
	} {
		html := renderComponent(t, DropdownMenu(DropdownMenuOpts{Label: "Actions", Items: []MenuItem{item}}))
		assert.NotContains(t, html, "hx-confirm",
			"a %s item cannot show a confirmation, so it must not claim one", name)
	}

	// A boosted link is the exception: htmx handles its navigation, so the
	// prompt does gate something.
	boosted := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{{Label: "Leave", Href: "/y", Confirm: "Discard changes?", HX: HX{Boost: true}}},
	}))
	assert.Contains(t, boosted, `hx-confirm="Discard changes?"`)
}

// A menu item that acts is a button; one that navigates is a link. Rendering an
// acting item as <a href=""> makes it a link to the current page, so a click
// before HTMX has loaded reloads the page instead of doing nothing.
func TestActingMenuItemIsAButtonNotAnEmptyLink(t *testing.T) {
	html := renderComponent(t, DropdownMenu(DropdownMenuOpts{
		Label: "Actions",
		Items: []MenuItem{
			{Label: "Open", Href: "/projects/1"},
			{Label: "Delete", Kind: KindDanger, Confirm: "Sure?", HX: HX{Delete: "/projects/1"}},
		},
	}))

	assert.NotContains(t, html, `href=""`,
		"an empty href is a link to the current page")
	assert.Contains(t, html, `href="/projects/1"`, "a navigating item stays a link")
	assert.Contains(t, html, `<button type="button"`, "an acting item must be a button")
	assert.Contains(t, html, `hx-delete="/projects/1"`)
}
