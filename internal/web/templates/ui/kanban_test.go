package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boardOpts() KanbanOpts {
	return KanbanOpts{
		ID: "board", MoveURL: "/board/move", Target: "#content",
		Columns: []KanbanColumnData{
			{ID: "todo", Title: "To do", Count: 1, Cards: []KanbanCardData{
				{ID: "c1", Title: "Wire the seam"},
			}},
			{ID: "doing", Title: "In progress", Count: 0},
			{ID: "done", Title: "Shipped", Count: 0},
		},
	}
}

// The move menu is the interface. A board operable only by dragging excludes
// keyboard users, screen reader users, and anyone on a touch screen where a drag
// competes with scrolling.
func TestEveryCardCanBeMovedWithoutDragging(t *testing.T) {
	html := renderComponent(t, Kanban(boardOpts()))

	assert.Contains(t, html, "Move to In progress")
	assert.Contains(t, html, "Move to Shipped")
	// Real menu buttons, reachable by keyboard.
	assert.Contains(t, html, `data-ui-menu-trigger`)
}

// Offering "move to where it already is" is a command that does nothing, and a
// menu full of no-ops trains the user to distrust it.
func TestMoveMenuOmitsTheCurrentColumn(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "Wire the seam"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.NotContains(t, html, "Move to To do")
	assert.Contains(t, html, "Move to In progress")
}

// Both paths must post the same payload to the same endpoint. Two request
// shapes would let the drag and the menu disagree about what a move is.
func TestDragAndMenuShareOneEndpointAndPayload(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "Wire the seam"},
		From: "todo", MoveURL: "/board/move", Target: "#content",
		Columns: boardOpts().Columns,
	}))

	// The form the drag fills in.
	assert.Contains(t, html, `data-kanban-form`)
	assert.Contains(t, html, `name="card" value="c1"`)
	assert.Contains(t, html, `name="from" value="todo"`)
	assert.Contains(t, html, `name="to"`)
	// The menu posts the same fields to the same URL.
	assert.Contains(t, html, `hx-post="/board/move"`)
	assert.Contains(t, html, `hx-vals`)
}

// The form has a real method and action, so a move works with no script at all.
// That fallback is what makes adding the drag safe.
func TestMoveFormWorksWithoutScript(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "T"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, `method="post"`)
	assert.Contains(t, html, `action="/board/move"`)
}

// A method and an action are not a fallback on their own: a form with nothing
// submittable in it cannot be sent. Each destination is a submit control naming
// itself, which is what makes a move one click with no script running.
func TestMoveDestinationsAreSubmitControls(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "T"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, `<button type="submit" name="to" value="doing"`)
	assert.Contains(t, html, `<button type="submit" name="to" value="done"`)
	// Visible in the markup. The controller hides the form once the card menu is
	// operable; a form hidden by the server is a fallback nobody can reach.
	assert.NotContains(t, html, `class="hidden"`)
}

// Exactly one destination may reach the server. The field a drop writes carries
// the same name as those buttons, so shipping it enabled would send a blank "to"
// alongside the chosen one - and the server reads the first.
func TestOnlyTheChosenDestinationIsSubmitted(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "T"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, `name="to" value="" data-kanban-to disabled`)
}

// Every card offers the same destinations, so the buttons are told apart by the
// group they sit in rather than by their own labels - which have to keep naming
// the destination, not the card.
func TestMoveButtonsAreGroupedUnderTheirCard(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "Wire the seam"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, `role="group" aria-label="Move Wire the seam"`)
}

// The server decides. Swapping the board rather than the card is what lets a
// rejected move put the card back where it was.
func TestRejectedMoveCanRerenderTheBoard(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "T"},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, `hx-swap="outerMorph"`)
	// With no explicit target the swap still reaches the whole board, never just
	// the card that moved.
	assert.Contains(t, html, "closest [data-ui=kanban]")
}

// Columns are labelled regions holding lists, so a screen-reader user can tell
// where one column ends and how many cards it holds.
func TestColumnsAreLabelledListsNotBareDivs(t *testing.T) {
	html := renderComponent(t, Kanban(boardOpts()))

	assert.Contains(t, html, "<section")
	assert.Contains(t, html, `aria-labelledby="todo-title"`)
	assert.Contains(t, html, "<ul")
	assert.Contains(t, html, "<li")
}

// Forty menus called "Actions" are indistinguishable in a screen reader's
// element list.
func TestCardMenusAreNamedAfterTheirCard(t *testing.T) {
	html := renderComponent(t, Kanban(boardOpts()))

	assert.Contains(t, html, "Actions for Wire the seam")
}

// A count that only exists as a colour tells a screen-reader user nothing, and
// tells a colourblind user nothing either.
func TestColumnStatesItsCountAndLimit(t *testing.T) {
	html := renderComponent(t, KanbanColumn(KanbanColumnOpts{
		Column: KanbanColumnData{ID: "doing", Title: "In progress", Count: 3, Limit: 2},
	}))

	assert.Contains(t, html, "3 of 2")
	// Over the limit is stated in words, not signalled by a tint.
	assert.Contains(t, html, "Over the limit of 2.")
}

// A limit that has not been exceeded must not announce anything: a warning
// present on every render is a warning nobody reads.
func TestWithinLimitSaysNothing(t *testing.T) {
	html := renderComponent(t, KanbanColumn(KanbanColumnOpts{
		Column: KanbanColumnData{ID: "doing", Title: "In progress", Count: 1, Limit: 2},
	}))

	assert.NotContains(t, html, "Over the limit")
	assert.Contains(t, html, "1 of 2")
}

// An empty column must say so. Blank space is indistinguishable from a column
// that failed to render.
func TestEmptyColumnExplainsItself(t *testing.T) {
	html := renderComponent(t, KanbanColumn(KanbanColumnOpts{
		Column: KanbanColumnData{ID: "done", Title: "Shipped"},
	}))

	assert.Contains(t, html, "Nothing here yet.")
}

// No move URL means no move controls: a menu of moves that post nowhere is worse
// than a card with no menu.
func TestNoMoveURLRendersNoMoveControls(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card:    KanbanCardData{ID: "c1", Title: "T"},
		From:    "todo",
		Columns: boardOpts().Columns,
	}))

	assert.NotContains(t, html, "Move to")
	assert.NotContains(t, html, "data-kanban-form")
}

// The engine is declared on the board, so the loader fetches Sortable only when
// a board is actually on the page.
func TestBoardDeclaresItsEngine(t *testing.T) {
	html := renderComponent(t, Kanban(boardOpts()))

	assert.Contains(t, html, `data-ui-engine="sortablejs"`)
}

// A caller's own commands and the moves must not run together into one
// undifferentiated list.
func TestCallerCommandsAreSeparatedFromMoves(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{
			ID: "c1", Title: "T",
			Menu: []MenuItem{{Label: "Rename", Href: "/rename"}},
		},
		From: "todo", MoveURL: "/board/move",
		Columns: boardOpts().Columns,
	}))

	assert.Contains(t, html, "Rename")
	require.Equal(t, 1, strings.Count(html, `role="separator"`))
}

// An empty badge renders a blank pill, which reads as a component that failed.
// The pointer is what lets a caller mean "no badge".
func TestCardWithoutBadgeRendersNoBadge(t *testing.T) {
	html := renderComponent(t, KanbanCard(KanbanCardOpts{
		Card: KanbanCardData{ID: "c1", Title: "T"}, From: "todo",
	}))

	assert.NotContains(t, html, `data-ui="badge"`)
}
