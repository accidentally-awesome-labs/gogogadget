package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func paletteOpts() CommandPaletteOpts {
	return CommandPaletteOpts{
		ID: "p", Label: "Search commands",
		SearchURL: "/search/commands", FallbackURL: "/search", Target: "#content",
		Groups: []CommandGroupData{
			{Title: "Navigate", Items: []CommandItemData{
				{ID: "i1", Label: "Go to projects", Href: "/app/projects"},
			}},
		},
	}
}

// The roles are applied by the controller that implements them. An input
// claiming role="combobox" with no active descendant and no key handling
// promises a listbox that never responds - worse than a plain search field.
func TestPaletteDoesNotClaimComboboxInMarkup(t *testing.T) {
	html := renderComponent(t, CommandPalette(paletteOpts()))

	assert.NotContains(t, html, `role="combobox"`)
	assert.NotContains(t, html, `role="listbox"`)
	assert.NotContains(t, html, "aria-activedescendant")
}

// With no script the palette must still be a working search form, or it is a
// dead input.
func TestPaletteFallsBackToARealSearchForm(t *testing.T) {
	html := renderComponent(t, CommandPalette(paletteOpts()))

	assert.Contains(t, html, `method="get"`)
	assert.Contains(t, html, `action="/search"`)
	assert.Contains(t, html, `name="q"`)
	assert.Contains(t, html, `type="submit"`)
}

// The dialog closes with no JavaScript: form method="dialog" is what makes it
// safe to open in the first place.
func TestDialogClosesWithoutScript(t *testing.T) {
	html := renderComponent(t, CommandPalette(paletteOpts()))

	assert.Contains(t, html, "<dialog")
	assert.Contains(t, html, `method="dialog"`)
}

// A keyboard-only affordance is undiscoverable, so the shortcut is not the only
// way in.
func TestPaletteHasAVisibleTrigger(t *testing.T) {
	html := renderComponent(t, CommandPalette(paletteOpts()))

	assert.Contains(t, html, "data-command-open")
	assert.Contains(t, html, `aria-haspopup="dialog"`)
	// And the shortcut is advertised where a pointer user can find it.
	assert.Contains(t, html, "<kbd")
}

// Search is server-owned, so the palette can find things that were never sent
// to the browser.
func TestSearchGoesToTheServer(t *testing.T) {
	html := renderComponent(t, CommandPalette(paletteOpts()))

	assert.Contains(t, html, `hx-get="/search/commands"`)
	assert.Contains(t, html, "delay:200ms")
	assert.Contains(t, html, `hx-target="#p-results"`)
}

// Every command is a link. A div with a click handler is unreachable by
// keyboard, un-middle-clickable, and absent from a screen reader's link list.
func TestEveryCommandIsALink(t *testing.T) {
	html := renderComponent(t, CommandItem(CommandItemOpts{
		Item: CommandItemData{ID: "i1", Label: "Go to projects", Href: "/app/projects"},
	}))

	assert.Contains(t, html, `href="/app/projects"`)
	assert.NotContains(t, html, "<button")
	assert.NotContains(t, html, "onclick")
}

// The group's title labels its list, so commands are announced as belonging to
// it rather than as one flat run.
func TestGroupTitleLabelsItsList(t *testing.T) {
	html := renderComponent(t, CommandGroup(CommandGroupOpts{
		Group: CommandGroupData{Title: "Navigate", Items: []CommandItemData{
			{ID: "i1", Label: "A", Href: "/a"},
		}},
	}))

	assert.Contains(t, html, `id="cmd-group-navigate"`)
	assert.Contains(t, html, `aria-labelledby="cmd-group-navigate"`)
}

// A title with spaces or punctuation must still yield a usable id reference. A
// raw title produces an aria-labelledby that resolves to nothing, which looks
// correct in the markup and fails in the browser.
func TestGroupIDIsAValidReference(t *testing.T) {
	html := renderComponent(t, CommandGroup(CommandGroupOpts{
		Group: CommandGroupData{Title: "Recent & saved!", Items: nil},
	}))

	require.Contains(t, html, `id="cmd-group-recent-saved"`)
	assert.NotContains(t, html, "Recent & saved!\"")
}

// An empty palette must say so. A blank panel reads as a component that failed
// to load.
func TestEmptyPaletteExplainsItself(t *testing.T) {
	opts := paletteOpts()
	opts.Groups = nil
	html := renderComponent(t, CommandPalette(opts))

	assert.Contains(t, html, "No commands yet.")
}

// A displayed shortcut that nothing listens for is a lie, so the binding belongs
// to the surface that owns the key - the component only shows it.
func TestShortcutIsDisplayedNotBound(t *testing.T) {
	html := renderComponent(t, CommandItem(CommandItemOpts{
		Item: CommandItemData{ID: "i", Label: "A", Href: "/a", Shortcut: "G P"},
	}))

	assert.Contains(t, html, "G P")
	assert.Equal(t, 1, strings.Count(html, "<kbd"))
	assert.NotContains(t, html, "accesskey")
}
