package ui

// Shared data contracts. These are data types, not renderers: several
// components read the same shape, so the shape is declared once here in the
// required core rather than per consumer. A consumer that needed its own field
// would fork the contract and silently break the others.

// Column is one column in a tabular view, read by Table, DataTable, DataGrid
// and TreeGrid.
type Column struct {
	Key, Label string
	// Width is a CSS length for one column (e.g. "8rem"), not the container
	// measure enum: a table column is sized in absolute terms.
	Width string
	Align Align
	// Sortable marks the column as a sort target; Numeric right-aligns figures
	// so digits line up by place value.
	Sortable, Numeric bool
	// HideBelow drops the column under a breakpoint. Without it a wide table
	// can only scroll horizontally on a phone, which hides data behind a
	// gesture rather than choosing what matters.
	HideBelow Breakpoint
}

// MenuItem is one entry in a menu, read by DropdownMenu, ContextMenu,
// RowActions and Kanban.
type MenuItem struct {
	Label, Href string
	// Icon is the typed registry name, so a menu cannot reference an icon that
	// does not exist.
	Icon IconName
	Kind Kind
	// Disabled keeps the item visible but inert - a command that vanishes when
	// unavailable teaches the user nothing about why.
	Disabled bool
	// Separator makes this a divider rather than a command: it carries no
	// label, href or handler, and renders as a separator role.
	Separator bool
	// Confirm is the prompt shown before the action runs. It lives in the
	// contract because a destructive item must not depend on the caller
	// remembering to add one.
	Confirm string
	// HX issues the request for items that act rather than navigate.
	HX    HX
	Attrs Attrs
}

// Option is one choice in a select, checkbox group or radio group.
type Option struct {
	Value    string
	Label    string
	Group    string
	Disabled bool
	Selected bool
}

func gapClass(gap Gap) string {
	switch gap.Value() {
	case GapNone:
		return "gap-0"
	case GapXS:
		return "gap-1"
	case GapSM:
		return "gap-2"
	case GapLG:
		return "gap-6"
	case GapXL:
		return "gap-8"
	}
	return "gap-4"
}
