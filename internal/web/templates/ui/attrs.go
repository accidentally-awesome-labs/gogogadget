package ui

// Attrs is the one attribute bundle every ui component accepts. Components
// merge these into a single templ.Attributes map and spread it on their root
// element. There is no arbitrary-attribute escape hatch: components own their
// semantic attributes (role, aria-*, tabindex, type, base class) and callers
// cannot override them.
type Attrs struct {
	ID     string
	Class  string
	TestID string
	Title  string
	// Decorative removes the element and its whole subtree from the
	// accessibility tree AND from the tab order. It is deliberately not named
	// AriaHidden and deliberately not a bare aria-hidden: aria-hidden alone on
	// a focusable element leaves the control tabbable while hiding it from
	// assistive technology, which strands a screen-reader user on a control
	// that announces nothing - the worst outcome of any accessibility change.
	// Emitting inert alongside it makes the two statements impossible to
	// disagree: inert removes descendants from the tab order, so a caller who
	// marks something decorative cannot accidentally hide a live control while
	// leaving it reachable. Use it for filler that carries no information a
	// non-sighted reader needs - a screenshot placeholder, a repeated
	// glyph - never to quiet a control that does something.
	Decorative bool
	Data       map[string]string
	Alpine     Alpine
	HX         HX
}

// Alpine carries CSP-safe named Alpine directives. Only the listed directives
// are settable; arbitrary x-* attributes are not exposed.
type Alpine struct {
	Data  string
	Show  string
	Text  string
	Model string
	Ref   string
	Trap  string
	If    string
	For   string
	Key   string
	Cloak bool
	Bind  map[string]string
	On    map[string]string
}

// HX carries CSP-safe named HTMX attributes.
type HX struct {
	Get, Post, Put, Patch, Delete string
	Target, Select, Swap, Trigger string
	Sync, Include, Indicator      string
	PushURL                       string
	Confirm                       string
	Encoding                      string
	Vals                          map[string]string
	Headers                       map[string]string
	Boost                         bool
	Disable                       bool
	HistoryElt                    bool
}
