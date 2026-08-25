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

// Gap is the spacing scale between siblings. The zero value is the standard
// gap, not flush: stacked content wants breathing room by default, and every
// existing caller that omits a gap already renders spaced. Flush is spelled
// explicitly as GapNone so it reads as a choice at the call site.
type Gap string

const (
	GapNone Gap = "none"
	GapXS   Gap = "xs"
	GapSM   Gap = "sm"
	GapMD   Gap = "md"
	GapLG   Gap = "lg"
	GapXL   Gap = "xl"
)

var Gaps = []Gap{GapNone, GapXS, GapSM, GapMD, GapLG, GapXL}

func (g Gap) Value() Gap  { return normalize(g, Gaps, GapMD) }
func (g Gap) Valid() bool { return contains(g, Gaps) }

// Padding is the inset scale, and follows Gap: the zero value is the standard
// inset, with flush spelled explicitly.
type Padding string

const (
	PaddingNone Padding = "none"
	PaddingSM   Padding = "sm"
	PaddingMD   Padding = "md"
	PaddingLG   Padding = "lg"
)

var Paddings = []Padding{PaddingNone, PaddingSM, PaddingMD, PaddingLG}

func (p Padding) Value() Padding { return normalize(p, Paddings, PaddingMD) }
func (p Padding) Valid() bool    { return contains(p, Paddings) }

// Width is the measure a container constrains content to. Unlike Gap, there is
// no meaningful empty: an unbounded line length is unreadable, so an unset
// width normalizes to the standard page measure.
type Width string

// Only the measures the token layer actually defines are declared here: a
// "wide" member with no --container-wide token would render unconstrained while
// claiming to be constrained.
const (
	WidthNarrow Width = "narrow"
	WidthPage   Width = "page"
	WidthFull   Width = "full"
)

var Widths = []Width{WidthNarrow, WidthPage, WidthFull}

func (w Width) Value() Width { return normalize(w, Widths, WidthPage) }
func (w Width) Valid() bool  { return contains(w, Widths) }

// Height is the vertical sizing intent for a media or scroll region.
type Height string

const (
	HeightAuto   Height = "auto"
	HeightSM     Height = "sm"
	HeightMD     Height = "md"
	HeightLG     Height = "lg"
	HeightScreen Height = "screen"
)

var Heights = []Height{HeightAuto, HeightSM, HeightMD, HeightLG, HeightScreen}

func (h Height) Value() Height { return normalize(h, Heights, HeightAuto) }
func (h Height) Valid() bool   { return contains(h, Heights) }

// Ratio is the aspect ratio a media box reserves. Reserving the box before the
// asset loads is what stops layout shift, so "auto" is the honest name for
// declining to reserve rather than a default that hides the cost.
type Ratio string

const (
	RatioAuto     Ratio = "auto"
	RatioSquare   Ratio = "square"
	RatioVideo    Ratio = "video"
	RatioWide     Ratio = "wide"
	RatioPortrait Ratio = "portrait"
)

var Ratios = []Ratio{RatioAuto, RatioSquare, RatioVideo, RatioWide, RatioPortrait}

func (r Ratio) Value() Ratio { return normalize(r, Ratios, RatioAuto) }
func (r Ratio) Valid() bool  { return contains(r, Ratios) }

// InputType is the HTML input type a text field renders. It is closed because
// an unrecognised type silently degrades to text in the browser while dropping
// the validation and mobile keyboard the caller asked for.
type InputType string

const (
	InputTypeText     InputType = "text"
	InputTypeEmail    InputType = "email"
	InputTypePassword InputType = "password"
	InputTypeURL      InputType = "url"
	InputTypeTel      InputType = "tel"
	InputTypeNumber   InputType = "number"
	InputTypeSearch   InputType = "search"
	InputTypeDate     InputType = "date"
)

var InputTypes = []InputType{
	InputTypeText, InputTypeEmail, InputTypePassword, InputTypeURL,
	InputTypeTel, InputTypeNumber, InputTypeSearch, InputTypeDate,
}

func (i InputType) Value() InputType { return normalize(i, InputTypes, InputTypeText) }
func (i InputType) Valid() bool      { return contains(i, InputTypes) }

// Side is the edge an element attaches to.
type Side string

const (
	SideTop    Side = "top"
	SideRight  Side = "right"
	SideBottom Side = "bottom"
	SideLeft   Side = "left"
)

var Sides = []Side{SideTop, SideRight, SideBottom, SideLeft}

func (s Side) Value() Side { return normalize(s, Sides, SideTop) }
func (s Side) Valid() bool { return contains(s, Sides) }

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
