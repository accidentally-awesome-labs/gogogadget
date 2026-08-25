package ui

import "github.com/a-h/templ"

// root builds the single attribute map a component spreads on its root element.
//
// Three properties matter and none of them are optional:
//
//   - data-ui carries the component name on every root, which is what lets the
//     gallery-coverage check compare rendered components against the installed
//     registry instead of trusting a hand-kept list.
//   - the component's own base class always survives; a caller's Class is
//     appended, never substituted, so no caller can drop `badge` from a Badge.
//   - a caller cannot set role, aria-*, tabindex or type through Attrs at all,
//     because Attrs has no arbitrary-attribute map. Components that need one
//     pass it through rootWith, where the component decides the value.
func root(name, baseClass string, a Attrs) templ.Attributes {
	out := attributes(a)
	out["data-ui"] = name
	if baseClass != "" {
		out["class"] = mergeClasses(baseClass, a.Class)
	}
	return out
}

// rootWith is root plus component-owned attributes appended in pairs. The
// component supplies them after the caller's attributes are flattened, so a
// component-owned semantic attribute always wins.
func rootWith(name, baseClass string, a Attrs, pairs ...string) templ.Attributes {
	out := root(name, baseClass, a)
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			continue
		}
		out[pairs[i]] = pairs[i+1]
	}
	return out
}

// mergeClasses joins a component's own classes with the caller's. Base first so
// a component class is never shadowed by a utility a caller appended.
func mergeClasses(base, extra string) string {
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + " " + extra
	}
}

// withClass returns a copy of a with extra prepended to its class list. Used by
// a component that wraps another and needs to contribute styling without
// discarding what the caller asked for.
func withClass(a Attrs, extra string) Attrs {
	a.Class = mergeClasses(extra, a.Class)
	return a
}

// defaultTestID fills TestID only when the caller left it empty, preserving the
// historical default test ids Playwright contracts already assert on.
func defaultTestID(a Attrs, fallback string) Attrs {
	if a.TestID == "" {
		a.TestID = fallback
	}
	return a
}

// noticeRole falls back to the accessible default rather than emitting an empty
// role attribute. "status" is the safe default: it announces politely, where a
// wrong "alert" interrupts a screen-reader user mid-sentence.
func noticeRole(role string) string {
	switch role {
	case "alert", "status":
		return role
	default:
		return "status"
	}
}

// clampPercent keeps a meter inside its track. An out-of-range value is a
// caller bug, but rendering a bar wider than its container is a visual break in
// production, so it is clamped rather than trusted.
func clampPercent(pct int) int {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// inputType defaults an unset control type to text rather than emitting
// type="", which browsers treat as text anyway but which reads as a bug.
// NormalizeKind maps an unset or unrecognised Kind onto neutral. An unknown
// value renders the neutral component rather than a class-less element: a bare
// `badge` with no colour reads as a visible bug, which is the point — it is
// better than silently looking like the brand default.
func NormalizeKind(k Kind) Kind {
	for _, known := range Kinds {
		if k == known {
			return k
		}
	}
	return KindNeutral
}

// Valid reports whether k is one of the declared kinds.
func (k Kind) Valid() bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}
