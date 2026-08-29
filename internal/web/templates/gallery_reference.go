package templates

import (
	"strings"

	"github.com/gogogadget/gogogadget/internal/web/templates/ui"
)

// The reference surface reads the generated registries and nothing else. A
// second, hand-kept inventory of signatures and guidance is exactly what rots:
// it agrees with the code on the day it is written and never again. So every
// string on these pages comes from the module manifest that installed the
// component, and a gap in the manifest is rendered as a stated gap rather than
// silently omitted - an absent section is indistinguishable from a page that
// failed to render, and nobody files a bug against a section they never saw.

// GalleryContext is the axis set an example is rendered under. The reference
// pages read it from the query string, so a reviewer can link to the exact
// state, locale, direction and density they are questioning instead of
// describing the steps to reach it.
type GalleryContext struct {
	State       string
	Locale      string
	Direction   string
	Density     ui.Density
	LongContent bool
	// Page is the 1-based page a paged surface is showing. It exists so a
	// scenario's pager is a working control rather than links that render the
	// same rows: a pager that does not move is worse than no pager, because it
	// looks like the data ended.
	Page int
}

// The stated substitutes for missing manifest data. Each names the file that
// would fix it, because the reader of this page is usually the person who can.
const (
	referenceNoSummary   = "No summary declared in this module's manifest."
	referenceNoSignature = "No signature declared in this module's manifest."
	referenceNoGuidance  = "No usage guidance declared in this module's manifest."
	referenceNoStates    = "No rendering states declared in this module's manifest."
	// referenceStatesNote names the states no manifest declares and says why.
	// Hover and focus are applied by the browser, so no renderer can produce
	// one and declaring it would name a state this page cannot show; locale,
	// direction, density and long content apply to every component at once, so
	// they are context controls rather than per-component states.
	referenceStatesNote = "Hover and focus are applied by the browser, not rendered here. " +
		"Locale, direction, density and long content are context controls that apply to every component."
	// The absence of a keyboard contract is a real fact about a component, not
	// missing data: most components render elements whose key handling is the
	// browser's. Saying so is what lets a reader stop looking.
	referenceNoKeyboard = "Adds no key handling. Keyboard behaviour is the browser's own for the elements it renders."
)

// referenceView is one component's reference plus the strings derived from it.
//
// Derivation happens once, here, so the template holds no logic and no string
// is computed twice while rendering the page that shows it.
type referenceView struct {
	Name      string
	Family    ui.GalleryFamily
	Summary   string
	Signature string
	// Usage is the call a reader can paste. Empty when the signature is absent
	// or unparseable: a half-built call is worse than none, because it looks
	// authoritative.
	Usage    string
	Guidance string
	// Keyboard is empty when the component declares no key handling; the
	// template states that rather than dropping the section.
	Keyboard string
	States   []string
	Facts    []ui.DescriptionItem
}

// referenceViewOf assembles what the reference page renders for one installed
// component.
//
// The component registry and the reference registry are generated from the same
// manifests in the same order, so a component with no reference row means the
// manifest declared the component without documenting it. That renders as a
// page of stated gaps, which is the only outcome that gets the manifest fixed.
func referenceViewOf(component ui.Component) referenceView {
	reference, _ := ui.ReferenceFor(component.Name)
	return referenceView{
		Name:      component.Name,
		Family:    component.Family,
		Summary:   fallbackText(reference.Summary, referenceNoSummary),
		Signature: fallbackText(reference.Signature, referenceNoSignature),
		Usage:     referenceUsage(reference.Signature),
		Guidance:  fallbackText(reference.Guidance, referenceNoGuidance),
		Keyboard:  reference.Keyboard,
		States:    reference.States,
		Facts:     componentFacts(component),
	}
}

// fallbackText states the gap instead of leaving an empty region.
func fallbackText(value, absent string) string {
	if strings.TrimSpace(value) == "" {
		return absent
	}
	return value
}

// referenceUsage turns a declared signature into a call a reader can paste.
//
// The signature carries no package because every exported renderer lives in
// package ui - that is the compiler-enforced boundary the UI layer is built on -
// so the prefix is derivable rather than another manifest field to keep in sync.
func referenceUsage(signature string) string {
	name, opts := signatureParts(signature)
	switch {
	case name == "":
		return ""
	case opts == "":
		return "@ui." + name + "()"
	}
	return "@ui." + name + "(ui." + opts + "{})"
}

// signatureParts splits `templ Name(o NameOpts)` into its renderer and options
// type. It returns empty strings rather than guessing at a shape it does not
// recognise, so an unparseable signature suppresses the usage example instead of
// publishing a call that does not compile.
func signatureParts(signature string) (name, opts string) {
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(signature), "templ "))
	open := strings.IndexByte(rest, '(')
	end := strings.LastIndexByte(rest, ')')
	if open <= 0 || end <= open {
		return "", ""
	}
	name = strings.TrimSpace(rest[:open])
	// The parameter is `o NameOpts` by convention, but the type alone is also
	// legal Go, so the last field wins either way.
	params := strings.TrimSpace(rest[open+1 : end])
	if i := strings.LastIndexByte(params, ' '); i >= 0 {
		params = strings.TrimSpace(params[i+1:])
	}
	if strings.ContainsAny(name, " \t") {
		return "", ""
	}
	return name, params
}

// familyEntry pairs an installed component with its reference so the family
// page can state the runtime facts and the summary from one lookup each.
type familyEntry struct {
	Component ui.Component
	Summary   string
	Note      string
}

// familyEntries lists one family's components in the registry's module order.
func familyEntries(family ui.GalleryFamily) []familyEntry {
	components := ui.ComponentsInFamily(family)
	out := make([]familyEntry, 0, len(components))
	for _, component := range components {
		reference, _ := ui.ReferenceFor(component.Name)
		out = append(out, familyEntry{
			Component: component,
			Summary:   fallbackText(reference.Summary, referenceNoSummary),
			Note:      componentRuntimeNote(component),
		})
	}
	return out
}
