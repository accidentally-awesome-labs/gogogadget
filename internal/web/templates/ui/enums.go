package ui

// The closed enums every component option draws from.
//
// Two rules make these worth having. First, each is a distinct string type, so
// passing a size where an emphasis belongs does not compile — the whole reason
// not to use bare strings. Second, every type normalizes an unrecognised value
// to a defined default rather than rendering a class-less element, because a
// component missing its modifier class is a silent visual break while a
// component rendering its neutral default is merely wrong in an obvious way.
//
// Normalization deliberately does not "fix" case or trim spaces. "Danger" and
// "danger " are typos, and quietly accepting them would make the closed set a
// suggestion.

// Size is the control-scale axis.
type Size string

const (
	SizeXS Size = "xs"
	SizeSM Size = "sm"
	SizeMD Size = "md"
	SizeLG Size = "lg"
)

// Sizes is every size, smallest first.
var Sizes = []Size{SizeXS, SizeSM, SizeMD, SizeLG}

// Value normalizes an unset or unknown size to the medium default.
func (s Size) Value() Size { return normalize(s, Sizes, SizeMD) }

// Valid reports whether s is a declared size.
func (s Size) Valid() bool { return contains(s, Sizes) }

// Emphasis is the fill treatment shared by badges, buttons and notices.
type Emphasis string

const (
	EmphasisSubtle  Emphasis = "subtle"
	EmphasisSolid   Emphasis = "solid"
	EmphasisOutline Emphasis = "outline"
)

var Emphases = []Emphasis{EmphasisSubtle, EmphasisSolid, EmphasisOutline}

// Value normalizes to subtle: the quietest treatment is the safe default,
// because a control that shouts by accident is worse than one that whispers.
func (e Emphasis) Value() Emphasis { return normalize(e, Emphases, EmphasisSubtle) }

func (e Emphasis) Valid() bool { return contains(e, Emphases) }

// Action is the button intent axis.
type Action string

const (
	ActionGhost   Action = "ghost"
	ActionPrimary Action = "primary"
	ActionDanger  Action = "danger"
	ActionInverse Action = "inverse"
	ActionLink    Action = "link"
)

var Actions = []Action{ActionGhost, ActionPrimary, ActionDanger, ActionInverse, ActionLink}

// Value normalizes to ghost. Defaulting to primary or danger would let a typo
// promote a button's apparent importance, or dress a harmless action as
// destructive.
func (a Action) Value() Action { return normalize(a, Actions, ActionGhost) }

func (a Action) Valid() bool { return contains(a, Actions) }

// ButtonType is the HTML button type. It is a closed enum because the default
// in HTML is "submit": a button inside a form with a missing type submits it,
// which is the classic accidental-submit bug.
type ButtonType string

const (
	TypeButton ButtonType = "button"
	TypeSubmit ButtonType = "submit"
	TypeReset  ButtonType = "reset"
)

var ButtonTypes = []ButtonType{TypeButton, TypeSubmit, TypeReset}

// Value normalizes to button — the inert choice. A component cannot know that a
// caller meant to submit, and guessing wrong submits a form.
func (t ButtonType) Value() ButtonType { return normalize(t, ButtonTypes, TypeButton) }

func (t ButtonType) Valid() bool { return contains(t, ButtonTypes) }

// Density is the row-spacing axis for tables and lists.
type Density string

const (
	DensityComfortable Density = "comfortable"
	DensityCompact     Density = "compact"
)

var Densities = []Density{DensityComfortable, DensityCompact}

func (d Density) Value() Density { return normalize(d, Densities, DensityComfortable) }
func (d Density) Valid() bool    { return contains(d, Densities) }

// Orientation is the axis a group lays out along.
type Orientation string

const (
	OrientationHorizontal Orientation = "horizontal"
	OrientationVertical   Orientation = "vertical"
)

var Orientations = []Orientation{OrientationHorizontal, OrientationVertical}

func (o Orientation) Value() Orientation { return normalize(o, Orientations, OrientationHorizontal) }
func (o Orientation) Valid() bool        { return contains(o, Orientations) }

// SortDirection is a column's sort state. None is meaningful and is the
// default: most columns are unsorted, and defaulting to ascending would render
// a sort indicator on every column at once.
type SortDirection string

const (
	SortNone SortDirection = ""
	SortAsc  SortDirection = "asc"
	SortDesc SortDirection = "desc"
)

var SortDirections = []SortDirection{SortNone, SortAsc, SortDesc}

func (s SortDirection) Value() SortDirection { return normalize(s, SortDirections, SortNone) }
func (s SortDirection) Valid() bool          { return contains(s, SortDirections) }

// Align is the cell/content alignment axis.
type Align string

const (
	AlignStart  Align = "start"
	AlignCenter Align = "center"
	AlignEnd    Align = "end"
)

var Aligns = []Align{AlignStart, AlignCenter, AlignEnd}

func (a Align) Value() Align { return normalize(a, Aligns, AlignStart) }
func (a Align) Valid() bool  { return contains(a, Aligns) }

// Breakpoint is the responsive threshold a column or element hides below.
type Breakpoint string

const (
	BreakpointNone Breakpoint = ""
	BreakpointSM   Breakpoint = "sm"
	BreakpointMD   Breakpoint = "md"
	BreakpointLG   Breakpoint = "lg"
)

var Breakpoints = []Breakpoint{BreakpointNone, BreakpointSM, BreakpointMD, BreakpointLG}

func (b Breakpoint) Value() Breakpoint { return normalize(b, Breakpoints, BreakpointNone) }
func (b Breakpoint) Valid() bool       { return contains(b, Breakpoints) }

// Placement is where an overlay sits relative to its trigger.
type Placement string

const (
	PlacementTop    Placement = "top"
	PlacementRight  Placement = "right"
	PlacementBottom Placement = "bottom"
	PlacementLeft   Placement = "left"
)

var Placements = []Placement{PlacementTop, PlacementRight, PlacementBottom, PlacementLeft}

func (p Placement) Value() Placement { return normalize(p, Placements, PlacementBottom) }
func (p Placement) Valid() bool      { return contains(p, Placements) }

// enumValue is the constraint every closed enum in this file satisfies.
type enumValue interface {
	~string
}

// normalize returns value when it is declared, and fallback otherwise. One
// implementation so every enum answers an unknown value the same way.
func normalize[T enumValue](value T, declared []T, fallback T) T {
	if contains(value, declared) {
		return value
	}
	return fallback
}

func contains[T enumValue](value T, declared []T) bool {
	for _, known := range declared {
		if value == known {
			return true
		}
	}
	return false
}
