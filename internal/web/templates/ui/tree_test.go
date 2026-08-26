package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func treeNodes() []TreeNodeData {
	return []TreeNodeData{
		{ID: "a", Label: "internal", Expanded: true, Children: []TreeNodeData{
			{ID: "a1", Label: "web", Href: "/web"},
		}},
		{ID: "b", Label: "registry", HasChildren: true},
		{ID: "c", Label: "README", Href: "/readme"},
	}
}

// The tree is nested details, so every branch opens and every leaf is reachable
// with no script. A div-based tree would have to implement disclosure, keyboard
// and semantics at once and would offer nothing when any of them failed.
func TestTreeOpensWithoutScript(t *testing.T) {
	html := renderComponent(t, Tree(TreeOpts{ID: "t", Label: "Files", Nodes: treeNodes()}))

	assert.Contains(t, html, "<details")
	assert.Contains(t, html, "<summary")
	// The expanded branch is open on arrival, so a deep link lands with the
	// branch the server opened already showing.
	assert.Contains(t, html, "open")
	assert.NotContains(t, html, "onclick")
}

// Depth must be stated. Indentation conveys the shape to a sighted reader and
// nothing at all to a screen reader.
func TestTreeStatesItsDepth(t *testing.T) {
	html := renderComponent(t, Tree(TreeOpts{ID: "t", Label: "Files", Nodes: treeNodes()}))

	assert.Contains(t, html, `aria-level="1"`)
	assert.Contains(t, html, `aria-level="2"`)
}

// A branch whose children are not loaded still discloses. Waiting for the fetch
// to render the control leaves nothing for the user to act on.
func TestUnloadedBranchStillDiscloses(t *testing.T) {
	html := renderComponent(t, TreeNode(TreeNodeOpts{
		Node:  TreeNodeData{ID: "b", Label: "registry", HasChildren: true},
		Level: 1,
	}))

	assert.Contains(t, html, "<details")
	// And it says it is loading, because an open branch with nothing in it reads
	// as a leaf.
	assert.Contains(t, html, "Loading")
}

// A leaf with no destination is a label. Rendering it as a button promises an
// action that does not exist.
func TestLeafWithoutHrefIsNotAControl(t *testing.T) {
	html := renderComponent(t, TreeNode(TreeNodeOpts{
		Node:  TreeNodeData{ID: "x", Label: "Group"},
		Level: 1,
	}))

	assert.NotContains(t, html, "<button")
	assert.NotContains(t, html, "<a ")
	assert.Contains(t, html, "Group")
}

// The tree grid is a table because the data is rows and columns, and a table
// already reports both.
func TestTreeGridIsATableWithLevels(t *testing.T) {
	html := renderComponent(t, TreeGrid(TreeGridOpts{
		ID: "g", Label: "Modules",
		Columns: []Column{{Key: "name", Label: "Module"}, {Key: "n", Label: "Files"}},
		Nodes: []TreeNodeData{
			{ID: "p", Label: "Parent", Cells: []string{"3"}, Expanded: true, Children: []TreeNodeData{
				{ID: "c", Label: "Child", Cells: []string{"1"}},
			}},
		},
	}))

	assert.Contains(t, html, "<table")
	assert.Contains(t, html, `<caption class="sr-only">Modules</caption>`)
	assert.Contains(t, html, `aria-level="1"`)
	assert.Contains(t, html, `aria-level="2"`)
	// The row header names the row, so a cell read in isolation still says which
	// module it belongs to.
	assert.Contains(t, html, `scope="row"`)
}

// A branch must say whether it is open. Without aria-expanded it is
// indistinguishable from a leaf.
func TestTreeGridBranchesStateTheirExpansion(t *testing.T) {
	html := renderComponent(t, TreeGrid(TreeGridOpts{
		ID: "g", Label: "Modules",
		Columns: []Column{{Key: "name", Label: "Module"}},
		Nodes: []TreeNodeData{
			{ID: "p", Label: "Closed", HasChildren: true},
			{ID: "l", Label: "Leaf"},
		},
	}))

	require.Equal(t, 1, strings.Count(html, "aria-expanded"))
	assert.Contains(t, html, `aria-expanded="false"`)
}

// A collapsed branch's children must not be in the table: rows a sighted user
// cannot see must not be read out either.
func TestCollapsedBranchHidesItsRows(t *testing.T) {
	html := renderComponent(t, TreeGrid(TreeGridOpts{
		ID: "g", Label: "Modules",
		Columns: []Column{{Key: "name", Label: "Module"}},
		Nodes: []TreeNodeData{
			{ID: "p", Label: "Parent", Children: []TreeNodeData{{ID: "c", Label: "Hidden child"}}},
		},
	}))

	assert.NotContains(t, html, "Hidden child")
}
