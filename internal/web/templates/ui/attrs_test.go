package ui

import (
	"reflect"
	"strings"
	"testing"

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
