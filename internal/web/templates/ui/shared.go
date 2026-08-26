package ui

import "github.com/a-h/templ"

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

// menuItemAttrs builds the one attribute map a menu item's element spreads.
//
// MenuItem carried an Attrs field that no menu rendered, so no item could hold
// a data-testid, an id or a title - the caller wrote it, read it back in the
// struct literal, and never learned it was dropped. Every menu in the catalog
// (ContextMenu, Menubar, RowActions, Kanban) delegates to DropdownMenu, so
// merging here is what fixes all of them at once.
//
// The class is merged inside, which means the element must not also carry a
// literal class attribute: two class attributes on one tag is what a caller
// appending Attrs.Class would otherwise produce. extraClass is the branch's own
// contribution ("w-full text-left" on an acting item).
//
// This is for the active branches only. A disabled item must not go through it:
// applying item.HX to an aria-disabled element would make htmx fire the request
// the disabled state promises it will not.
func menuItemAttrs(item MenuItem, extraClass string) templ.Attributes {
	out := attributes(withClass(item.Attrs, mergeClasses(menuItemClass(item), extraClass)))
	applyHX(out, item.HX)
	if item.Confirm != "" {
		out["hx-confirm"] = item.Confirm
	}
	return out
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
