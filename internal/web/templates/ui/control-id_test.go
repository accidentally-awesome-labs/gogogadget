package ui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every form control hardcoded id={o.Name}. That is correct for a singleton
// form and wrong the moment a control repeats per row: /admin/users renders a
// name="role" select on every user and /admin/flags a name="rollout" input on
// every flag, so the page emitted one identical id dozens of times. `for=` and
// `aria-describedby` both resolve to the FIRST match, so every row's label and
// every row's error described row one. A label pointing at the wrong control is
// worse than no label at all.
//
// One helper resolves the id, and these tests prove every control routes
// through it. The set is DERIVED, not written down: a control is any installed
// renderer whose options declare both ID and Name and whose output submits
// under that name. The hand-written table this replaces named ten controls
// owned by ten other modules from ui-core's own payload — so it did not compile
// in a closure that installed ui-core without them — and it also silently
// exempted the other fifteen controls in the catalog.
func addressableControls(t *testing.T, id, name string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, renderer, fn)
		if !hasStringField(opts, "ID") || !hasStringField(opts, "Name") {
			continue
		}
		setOptsFields(t, renderer, opts, map[string]any{"ID": id, "Name": name})
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		// A renderer that declares Name without submitting under it is a
		// wrapper, not a control: Field draws the label and the error elements
		// around whatever the caller puts inside it.
		if !strings.Contains(html, `name="`+name+`"`) {
			continue
		}
		out[renderer] = html
	}
	require.GreaterOrEqual(t, len(out), 10,
		"only %d addressable controls were derived; the filter has collapsed, not the catalog", len(out))
	return out
}

// hasStringField reports whether an options value declares one settable string
// field, which is how a control is told apart from a wrapper before rendering.
func hasStringField(opts reflect.Value, name string) bool {
	field := opts.FieldByName(name)
	return field.IsValid() && field.Kind() == reflect.String
}

func TestEveryControlHonoursAnExplicitID(t *testing.T) {
	for component, html := range addressableControls(t, "role-42", "role") {
		assert.Containsf(t, html, `id="role-42"`,
			"%s ignored the explicit ID, so a per-row control still collides with every other row", component)
		assert.NotContainsf(t, html, `id="role"`,
			"%s emitted the name as an id as well; two id attributes on one element is a browser coin flip", component)
		assert.Containsf(t, html, `name="role"`,
			"%s must keep submitting under Name - the handler reads the name, not the id", component)
		assert.Equalf(t, 1, strings.Count(html, ` id="role-42"`),
			"%s emitted the resolved id more than once", component)
	}
}

// Name stays the default, because every existing caller depends on it and a
// singleton form addresses its control by field name.
func TestControlIDDefaultsToName(t *testing.T) {
	for component, html := range addressableControls(t, "", "email") {
		assert.Containsf(t, html, `id="email"`,
			"%s stopped defaulting its id to Name, which breaks every existing caller", component)
	}
}

// Attrs.ID is honoured as the fallback for a single-element control, so a caller
// who already set the id there keeps working - and must not suddenly emit two id
// attributes on the same element.
func TestAttrsIDIsHonouredWithoutDuplicating(t *testing.T) {
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, renderer, fn)
		if !hasStringField(opts, "ID") || !hasStringField(opts, "Name") {
			continue
		}
		setOptsFields(t, renderer, opts, map[string]any{"Name": "role"})
		opts.FieldByName("Attrs").FieldByName("ID").SetString("role-7")
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		if !strings.Contains(html, `name="role"`) {
			continue
		}
		assert.Containsf(t, html, `id="role-7"`,
			"%s ignores Attrs.ID, so a caller who already set the id there stops working", renderer)
		assert.Equalf(t, 1, strings.Count(html, ` id="role-7"`),
			"%s emitted the caller's id more than once", renderer)
	}
}

// A control's own aria-describedby must name the resolved id too, or the row's
// input points at the first row's hint. Derived over every installed control
// that accepts a Hint, rather than asserted on one of them.
//
// Only the reference is asserted here. Whether the hint ELEMENT exists is the
// wrapper's contract - Field draws it, and field_test.go asserts the two agree
// - so a control rendered on its own correctly names an element its container
// supplies.
func TestControlDescribedByFollowsTheResolvedID(t *testing.T) {
	checked := 0
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, renderer, fn)
		if !hasStringField(opts, "ID") || !hasStringField(opts, "Name") || !hasStringField(opts, "Hint") {
			continue
		}
		setOptsFields(t, renderer, opts, map[string]any{
			"ID": "rollout-beta", "Name": "rollout", "Hint": "Percent of accounts",
		})
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		if !strings.Contains(html, `name="rollout"`) {
			continue
		}
		checked++
		assert.Containsf(t, html, `aria-describedby="rollout-beta-hint"`,
			"%s renders a hint its control does not point at, so the row describes the first row's hint", renderer)
	}
	require.Greater(t, checked, 1, "no control with a Hint was derived; the filter has collapsed")
}
