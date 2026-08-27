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
	// Width is one column's starting measure as an absolute CSS length (e.g.
	// "8rem"), not the container measure enum: a timestamp column that must not
	// wrap knows its own width, and the spacing scale has no entry for it. It
	// renders as an inline width on the header cell - see columnWidthStyle for
	// why that is the same property the grid controller writes.
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
	// remembering to add one. It gates an htmx request and nothing else, so it
	// is honoured only on an item that issues one - see menuItemAttrs.
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
	// Only on an item that issues a request. hx-confirm is htmx's gate on an
	// htmx request: on a plain link or an inert button htmx never processes the
	// click, so the attribute renders, promises a prompt, and produces neither
	// the prompt nor the action. It was emitted unconditionally, and the rule
	// against it survived only as a comment two files away - which is exactly
	// how the Operations scenario shipped a destructive "Cancel" that did
	// nothing and asked nothing. The component refuses the combination rather
	// than rendering a promise it cannot keep.
	if item.Confirm != "" && menuItemRequests(item) {
		out["hx-confirm"] = item.Confirm
	}
	return out
}

// menuItemRequests reports whether htmx will process this item's activation.
//
// Both HX sources count: a caller may declare the request on the item or inside
// its Attrs, and both reach the same element.
//
// A verb is one way; a boosted link is the other - htmx handles a boosted
// anchor's navigation, so a confirmation on one does gate something. Target,
// swap and the rest are modifiers on a request that some other attribute has to
// declare, so they are deliberately not enough.
func menuItemRequests(item MenuItem) bool {
	for _, hx := range []HX{item.HX, item.Attrs.HX} {
		switch {
		case hx.Get != "", hx.Post != "", hx.Put != "", hx.Patch != "", hx.Delete != "":
			return true
		case hx.Boost && item.Href != "":
			return true
		}
	}
	return false
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

// inputClass maps the control-scale axis onto the .input component classes.
//
// One helper rather than a switch per control: fourteen form controls share the
// .input base, and a size axis reimplemented fourteen times is fourteen chances
// to disagree. It exists at all because pages were reaching past the typed API
// to write Attrs.Class: "input-xs" - which no test could see and no component
// could size consistently.
func inputClass(size Size) string {
	switch size.Value() {
	case SizeXS:
		return "input input-xs"
	case SizeSM:
		return "input input-sm"
	case SizeLG:
		return "input input-lg"
	}
	return "input"
}
