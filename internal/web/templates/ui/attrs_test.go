package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A declared option that is silently dropped is worse than a missing one: the
// caller writes it, reads it back in the struct literal, and never learns the
// attribute did not reach the element. Include, Sync, Indicator and PushURL were
// declared on HX and never emitted. This walks every field by reflection, so a
// field added without an emitter fails here instead of in production.
func TestEveryHXFieldReachesTheElement(t *testing.T) {
	hx := HX{
		Get: "/g", Post: "/p", Put: "/u", Patch: "/a", Delete: "/d",
		Target: "#t", Select: "#s", Swap: "outerHTML", Trigger: "click",
		Confirm: "sure?", Encoding: "multipart/form-data",
		Sync: "this:replace", Include: "[name=x]", Indicator: "spin",
		PushURL: "true",
		Vals:    map[string]string{"k": "v"},
		Headers: map[string]string{"H": "1"},
		Boost:   true, Disable: true, HistoryElt: true,
	}
	attrs := attributes(Attrs{HX: hx})

	value := reflect.ValueOf(hx)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		require.Falsef(t, value.Field(i).IsZero(),
			"HX.%s is zero in this fixture, so the test cannot prove it is emitted - set it", field.Name)

		want := "hx-" + hxAttrName(field.Name)
		_, ok := attrs[want]
		assert.Truef(t, ok,
			"HX.%s was set but no %q attribute was emitted, so the caller's instruction never reaches htmx", field.Name, want)
	}
}

// hxAttrName maps a field name onto htmx's kebab-case attribute.
func hxAttrName(field string) string {
	switch field {
	case "PushURL":
		return "push-url"
	case "HistoryElt":
		return "history-elt"
	}
	return strings.ToLower(field)
}

// A bare id is the common case and must become a selector; anything already
// selector-shaped must survive untouched, because "#" + ".loading" matches
// nothing and a request indicator that never appears is invisible to a reader
// and to a test alike.
func TestIndicatorAcceptsIdsAndSelectors(t *testing.T) {
	for value, want := range map[string]string{
		"spinner":          "#spinner",
		"#spinner":         "#spinner",
		".loading":         ".loading",
		"#row-1 .spinner":  "#row-1 .spinner",
		"[data-indicator]": "[data-indicator]",
	} {
		attrs := attributes(Attrs{HX: HX{Indicator: value}})
		assert.Equalf(t, want, attrs["hx-indicator"], "Indicator(%q)", value)
	}
}

// A decorative block must leave the accessibility tree AND the tab order
// together. aria-hidden alone on a focusable subtree is the worst outcome of any
// accessibility change: the control stays tabbable while announcing nothing, so
// a screen-reader user lands on it and is told nothing at all. inert is what
// makes the two statements impossible to disagree.
func TestDecorativeHidesAndDisablesTogether(t *testing.T) {
	attrs := attributes(Attrs{Decorative: true})
	assert.Equal(t, "true", attrs["aria-hidden"])
	assert.Equal(t, true, attrs["inert"],
		"aria-hidden without inert leaves a hidden subtree in the tab order")

	plain := attributes(Attrs{})
	assert.NotContains(t, plain, "aria-hidden",
		"aria-hidden=\"false\" is noise; an element that is not decorative carries neither attribute")
	assert.NotContains(t, plain, "inert")
}

// The capability has to survive the root builders, or a component's caller can
// set it and see nothing.
func TestDecorativeSurvivesTheRootBuilders(t *testing.T) {
	for name, attrs := range map[string]templ.Attributes{
		"root":     root("tile", "tile", Attrs{Decorative: true}),
		"rootWith": rootWith("tile", "tile", Attrs{Decorative: true}, "role", "presentation"),
	} {
		assert.Equalf(t, "true", attrs["aria-hidden"], "%s dropped Decorative", name)
		assert.Equalf(t, true, attrs["inert"], "%s dropped the inert half of Decorative", name)
	}
}

// HX has a reflection guard above; Attrs and Alpine had none, and they are
// enumerated by hand in attributes() and applyAlpine() exactly the same way. A
// field added to either reaches nothing, silently.
//
// That defect class landed three times in one day: MenuItem.Attrs was declared
// and rendered nowhere, four HX fields were declared and never emitted, and
// DropdownMenu's panel-id seed enumerated a subset of MenuItem's fields and so
// collided the moment a menu differed only in the fields it omitted. Attrs
// itself gained a field the same day (Decorative), and only a hand-written test
// would have caught a missing emitter for it.
//
// So the field list comes from the type and the expectation is explicit: a new
// field with no entry fails here rather than in a browser.
func TestEveryAttrsFieldReachesTheElement(t *testing.T) {
	// Each field paired with the attribute key that proves it landed.
	proves := map[string]string{
		"ID":         "id",
		"Class":      "class",
		"TestID":     "data-testid",
		"Title":      "title",
		"Decorative": "aria-hidden",
		"Data":       "data-row",
		"Alpine":     "x-data",
		"HX":         "hx-post",
	}

	full := Attrs{
		ID: "node", Class: "mt-1", TestID: "row-1", Title: "Row one",
		Decorative: true,
		Data:       map[string]string{"row": "1"},
		Alpine:     Alpine{Data: "uiMenu"},
		HX:         HX{Post: "/act"},
	}
	attrs := attributes(full)

	value := reflect.ValueOf(full)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		require.Falsef(t, value.Field(i).IsZero(),
			"Attrs.%s is zero in this fixture, so the test cannot prove it is emitted - set it", field.Name)

		want, ok := proves[field.Name]
		require.Truef(t, ok,
			"Attrs.%s has no entry in this table, so nothing proves it reaches the element - add one", field.Name)
		assert.Containsf(t, attrs, want,
			"Attrs.%s was set but no %q attribute was emitted, so the caller's option is dropped", field.Name, want)
	}
}

func TestEveryAlpineFieldReachesTheElement(t *testing.T) {
	proves := map[string]string{
		"Data": "x-data", "Show": "x-show", "Text": "x-text", "Model": "x-model",
		"Ref": "x-ref", "Trap": "x-trap", "If": "x-if", "For": "x-for",
		"Key": "x-key", "Cloak": "x-cloak",
		"Bind": ":disabled", "On": "@click",
	}

	full := Alpine{
		Data: "uiMenu", Show: "open", Text: "label", Model: "value",
		Ref: "panel", Trap: "open", If: "ready", For: "item in items",
		Key: "item.id", Cloak: true,
		Bind: map[string]string{"disabled": "busy"},
		On:   map[string]string{"click": "toggle"},
	}
	attrs := templ.Attributes{}
	applyAlpine(attrs, full)

	value := reflect.ValueOf(full)
	for i := 0; i < value.NumField(); i++ {
		field := value.Type().Field(i)
		require.Falsef(t, value.Field(i).IsZero(),
			"Alpine.%s is zero in this fixture, so the test cannot prove it is emitted - set it", field.Name)

		want, ok := proves[field.Name]
		require.Truef(t, ok,
			"Alpine.%s has no entry in this table, so nothing proves it reaches the element - add one", field.Name)
		assert.Containsf(t, attrs, want,
			"Alpine.%s was set but no %q attribute was emitted, so the caller's directive is dropped", field.Name, want)
	}
}
