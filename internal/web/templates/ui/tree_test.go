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

// Depth must reach the controller, but not as aria-level: that attribute is not
// allowed on a summary element, so emitting it is invalid markup rather than
// extra information. The controller applies the tree roles - and the level with
// them - once it is running, which is when the keyboard model those roles
// promise actually exists.
func TestTreeCarriesItsDepthAsData(t *testing.T) {
	html := renderComponent(t, Tree(TreeOpts{ID: "t", Label: "Files", Nodes: treeNodes()}))

	assert.Contains(t, html, `data-tree-level="1"`)
	assert.Contains(t, html, `data-tree-level="2"`)
	assert.NotContains(t, html, "aria-level")
	assert.NotContains(t, html, `role="treeitem"`)
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
	// Depth is stated in the row header's own text. aria-level is only valid on
	// treegrid rows, and this table does not claim that role because it has no
	// two-dimensional keyboard model to back it.
	assert.NotContains(t, html, "aria-level")
	assert.Contains(t, html, "level 2")
	// The row header names the row, so a cell read in isolation still says which
	// module it belongs to.
	assert.Contains(t, html, `scope="row"`)
}

// A branch must say whether it is open, and the caret that shows it is
// invisible to a screen reader. It is stated in text rather than with
// aria-expanded, which a plain table row may not carry.
func TestTreeGridBranchesStateTheirExpansion(t *testing.T) {
	html := renderComponent(t, TreeGrid(TreeGridOpts{
		ID: "g", Label: "Modules",
		Columns: []Column{{Key: "name", Label: "Module"}},
		Nodes: []TreeNodeData{
			{ID: "p", Label: "Closed", HasChildren: true},
			{ID: "l", Label: "Leaf"},
		},
	}))

	assert.NotContains(t, html, "aria-expanded")
	assert.Contains(t, html, "collapsed")
	// The leaf says nothing about expansion, because it has none.
	require.Equal(t, 1, strings.Count(html, "collapsed"))
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
