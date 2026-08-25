package ui

// Column is one column in a table.
type Column struct {
	Key, Label string
	// Width is a CSS length for one column (e.g. "8rem"), not the container
	// measure enum: a table column is sized in absolute terms.
	Width             string
	Align             Align
	Sortable, Numeric bool
}

// MenuItem is one entry in a menu.
type MenuItem struct {
	Label, Href string
	Kind        Kind
	Disabled    bool
	Attrs       Attrs
}

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
