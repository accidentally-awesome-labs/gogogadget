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
// owned by ten other modules from ui-core's own payload, so it did not compile
// in a closure that installed ui-core without them.
//
// It also exempted exactly ONE control: MarkdownEditor. An earlier note here
// said "the other fifteen controls in the catalog", which was wrong — the
// derivation now finds EIGHTEEN addressable controls in the whole catalog
// (ColorInput, Combobox, DateField, DateTimeField, FileDropzone, FileInput,
// MarkdownEditor, MultiSelect, NumberInput, OTPInput, PasswordInput,
// RangeInput, Select, SlugInput, TagsInput, TextInput, Textarea, TimeField),
// so eighteen against ten is the margin. The floor below stays at ten because
// ten is what the hand table named: it is there to catch a filter that has
// collapsed, and is not a census. (The bare word for that state is avoided
// deliberately - Tailwind scans this file, and a utility class name in prose
// adds a rule to static/app.css.)
//
// Eleven renderers carried Name and declared NO ID option. Seven of them also
// spread Attrs through root/rootWith while writing their own id={o.Name} on
// the SAME element — Textarea, PasswordInput, TagsInput, SlugInput, TimeField,
// OTPInput and RangeInput — so a caller who reached for Attrs.ID got two id
// attributes and the browser kept the component's. They now declare ID and
// resolve through controlID, so this derivation reaches them.
//
// The remaining four stay outside, deliberately:
//
//   - SearchInput renders no default id at all, because its field is addressed
//     through the form it sits in — hx-include="this", a real submit button, an
//     aria-label — rather than by a label's for=. It already emits at most one
//     id and already honours Attrs.ID. An ID option would make this table
//     demand a name-derived id on a control nothing points at.
//   - Checkbox and Switch render NO id on any element: the input sits inside
//     its <label>, so no for= has to resolve. "Exactly one id" is vacuous for
//     them, and giving them one is a product decision about their addressing
//     contract, not a repair.
//   - Composer's Attrs land on its <form>; the <textarea> inside gets its own
//     id and the sr-only <label for=> already resolves to it. Two Composers
//     sharing a Name collide, but that is the Name-uniqueness contract, not
//     invalid markup, and threading an ID into the nested Textarea is the same
//     product decision.
//
// None of that is what let the duplicate ship. A gate whose population is
// "declares a top-level ID" cannot reach a renderer with no ID, which is
// exactly the property the seven had. The invariant is therefore derived from
// behaviour instead, catalog-wide, in TestNoRendererEmitsTwoIDsOnOneElement —
// and deriving it that way immediately found the same defect in six renderers
// no review of the Name-bearing population would ever have sampled (Dialog,
// Drawer, AlertDialog, Panel, PanelHandle and Slide).
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

// Attrs.ID is honoured as the fallback for a single-element control, so a
// caller who already set the id there keeps working.
//
// This test does NOT police duplication, and that omission is the whole story
// of this file: it derived its population the same way addressableControls
// does, through "declares a top-level ID", which is precisely the property the
// six renderers that emitted TWO ids lacked. A gate whose population is a
// property the defect excludes cannot see the defect. Duplication is asserted
// from rendered behaviour instead, catalog-wide, in
// TestNoRendererEmitsTwoIDsOnOneElement.
func TestAttrsIDIsHonouredByEveryControl(t *testing.T) {
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
	}
}

// Both set at once is the case a caller lands in when a component gains an ID
// option after they had already reached for Attrs.ID, so it needs a stated
// answer rather than whichever attribute the browser happened to see first.
//
// The declared ID wins. For a form control that is the narrow reason: ID is
// the field the caller reached for deliberately and Attrs.ID is the generic
// escape hatch. Derived over every addressable control, so a new one cannot
// pick the other order.
//
// The claim is scoped to the element that SUBMITS, because a wrapping
// renderer legitimately spends the two ids on two different elements:
// FileDropzone and MarkdownEditor put the control's id on the inner field and
// the caller's Attrs.ID on the label or wrapper around it, with for= joining
// them. Nothing is duplicated and nothing is mislabelled there, so a blanket
// "Attrs.ID appears nowhere" would fail a correct component.
func TestADeclaredIDOutranksAttrsIDOnEveryControl(t *testing.T) {
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, renderer, fn)
		if !hasStringField(opts, "ID") || !hasStringField(opts, "Name") {
			continue
		}
		fields := map[string]any{"ID": "role-42", "Name": "role"}
		if hasStringField(opts, "Hint") {
			fields["Hint"] = "Percent of accounts"
		}
		setOptsFields(t, renderer, opts, fields)
		opts.FieldByName("Attrs").FieldByName("ID").SetString("escape-hatch")
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		if !strings.Contains(html, `name="role"`) {
			continue
		}
		control := submittingTag(t, renderer, html)
		assert.Containsf(t, control, `id="role-42"`,
			"%s let the generic Attrs.ID beat the ID field the caller reached for deliberately: %s", renderer, control)
		assert.NotContainsf(t, control, "escape-hatch",
			"%s kept Attrs.ID on the control as well, so the element and the things pointing at it disagree", renderer)
		assert.NotContainsf(t, html, "escape-hatch-",
			"%s derived a reference from the losing id, which names an element nothing renders", renderer)
		if strings.Contains(control, "aria-describedby") {
			assert.Containsf(t, control, `aria-describedby="role-42-hint"`,
				"%s describes an element derived from the losing id", renderer)
		}
	}
}

// submittingTag is the one start tag carrying name="role" - the element whose
// id a label's for= and the control's own aria-describedby have to agree with.
//
// The attribute name is matched whole. A loose substring finds
// MarkdownEditor's wrapper first, on data-editor-name="role", and would then
// assert the control's contract against a div that submits nothing.
func submittingTag(t *testing.T, renderer, html string) string {
	t.Helper()
	for _, tag := range startTags(html) {
		if tagHasAttr(tag, "name", "role") {
			return tag
		}
	}
	require.Failf(t, "no submitting element", "%s renders name=\"role\" outside any start tag", renderer)
	return ""
}

// tagHasAttr reports whether one start tag carries exactly this attribute at
// this value, requiring the separator so data-editor-name is not read as name.
func tagHasAttr(tag, attr, value string) bool {
	want := attr + `="` + value + `"`
	for i := 1; i+len(want) <= len(tag); i++ {
		if isTagSpace(tag[i-1]) && tag[i:i+len(want)] == want {
			return true
		}
	}
	return false
}

// The same precedence away from form controls, where the id is a HUB rather
// than one element attribute: aria-labelledby, aria-describedby, data-panel,
// data-carousel-slide and an Alpine close() argument are all derived from it,
// so whichever value wins has to win in every one of them at once. An element
// carrying Attrs.ID while every reference to it still named the declared ID is
// a silently mislabelled dialog, which is worse than a visible break.
//
// Derived, and it has to be: this is ui-core's own payload, so naming Dialog
// or Panel here would install a test that only compiles in a closure carrying
// those component modules — the exact defect the hand-written table this file
// replaced had.
//
// The claim is a SUBSET one, because a wrapping renderer legitimately spends
// the two ids on two elements: Collapsible, CommandPalette and HoverCard put
// their ID on an inner panel while Attrs.ID names the root, so "Attrs.ID
// appears nowhere" would be false for them. What is universally true is that
// adding Attrs.ID may only ADD an id — every id the declared ID produced,
// including the derived ones, must survive. If Attrs.ID outranked it, the
// declared value would vanish from the second render.
func TestAttrsIDNeverDisplacesADeclaredID(t *testing.T) {
	checked := 0
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		alone := idsRendered(t, renderer, fn, "declared", "")
		if len(alone) == 0 {
			continue
		}
		checked++
		beside := idsRendered(t, renderer, fn, "declared", "escape-hatch")
		for id := range alone {
			assert.Containsf(t, beside, id,
				"%s lost id %q once Attrs.ID was set as well, so the element and everything pointing at it disagree",
				renderer, id)
		}
	}
	require.Greater(t, checked, 20,
		"only %d renderers produced any id from a declared ID; the seeding has collapsed", checked)
}

// Routing the same value through Attrs.ID instead of the declared ID must
// produce the same ids, derivations included. A fallback that moved only the
// id attribute would leave aria-labelledby pointing at "-title", which names
// nothing at all.
//
// Scoped to the renderers where the two genuinely compete, and that is
// detected rather than declared: a component whose ID and Attrs.ID land on
// different elements produces MORE ids when both are set, so it is excluded by
// the same measurement that includes the id hubs.
func TestAttrsIDIsATrueFallbackForADeclaredID(t *testing.T) {
	checked := 0
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		alone := idsRendered(t, renderer, fn, "declared", "")
		if len(alone) == 0 || len(idsRendered(t, renderer, fn, "declared", "escape-hatch")) != len(alone) {
			continue
		}
		checked++
		assert.Equalf(t, alone, idsRendered(t, renderer, fn, "", "declared"),
			"%s honoured Attrs.ID on the element but not in what points at it", renderer)
	}
	require.Greater(t, checked, 20,
		"only %d renderers route one id through controlID; the measurement has collapsed", checked)
}

// idsRendered is the set of id attribute values one renderer emits when its
// declared id is set to id and its Attrs.ID to attrsID.
//
// "Its declared id" reaches one level into a data struct, because Panel and
// Slide declare theirs as PanelData.ID and SlideData.ID rather than on the
// options root. Reflecting for the field rather than naming the renderer keeps
// this payload free of every other module's symbols.
func idsRendered(t *testing.T, renderer string, fn reflect.Value, id, attrsID string) map[string]struct{} {
	t.Helper()
	opts := seededOpts(t, renderer, fn)
	seedDeclaredIDs(opts, id)
	if attrs := opts.FieldByName("Attrs"); attrs.IsValid() {
		if field := attrs.FieldByName("ID"); field.IsValid() && field.Kind() == reflect.String {
			field.SetString(attrsID)
		}
	}
	html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
	out := map[string]struct{}{}
	for _, tag := range startTags(html) {
		if value, ok := idValue(tag); ok && value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

// seedDeclaredIDs sets every settable string ID an options value exposes: its
// own, and the one inside each struct field it carries.
func seedDeclaredIDs(opts reflect.Value, id string) {
	if field := opts.FieldByName("ID"); field.IsValid() && field.Kind() == reflect.String {
		field.SetString(id)
	}
	for i := range opts.NumField() {
		field := opts.Field(i)
		if field.Kind() != reflect.Struct || opts.Type().Field(i).Name == "Attrs" {
			continue
		}
		if nested := field.FieldByName("ID"); nested.IsValid() && nested.Kind() == reflect.String && nested.CanSet() {
			nested.SetString(id)
		}
	}
}

// idValue is the value of a start tag's id attribute, and whether it has one.
func idValue(tag string) (string, bool) {
	for i := 1; i+4 <= len(tag); i++ {
		if !isTagSpace(tag[i-1]) || tag[i:i+4] != `id="` {
			continue
		}
		rest := tag[i+4:]
		if end := strings.IndexByte(rest, '"'); end >= 0 {
			return rest[:end], true
		}
	}
	return "", false
}

// Two id attributes on one element is invalid HTML, and the browser keeps the
// FIRST - so a component that spreads Attrs and then writes its own id wins
// against the caller silently, which is worse than refusing the option.
//
// The population is derived from what a renderer DOES, not from what its
// options declare: any renderer that renders id= at all must render exactly
// one per element. That needs no ID field and no Name field, so it covers the
// whole installed catalog rather than the subset that already got this right.
// Every options-shaped filter this file used before excluded the renderers
// carrying the defect.
//
// Every probe is exercised, not just the interesting one, because the pairing
// is where the collision lives: Attrs.ID alone is what a caller reaches for
// when no ID option exists, and ID-and-Attrs.ID together is what happens when
// one is added later.
//
// The fifth probe fills every collection reflectively (see fillOpts), because
// the population being universal is not the same as the STIMULUS being
// universal. rendererSeeds is a 13-entry hand table, and an element rendered
// only inside a branch its values never reach was never scanned: a span with
// two id attributes inside DropdownMenu's o.Items loop passed every probe,
// because DropdownMenu is seeded with an ID and a Label and no Items.
//
// One more thing worth stating here rather than leaving to be rediscovered: a
// renderer that renders NO element passes this loop silently, because there
// are no start tags to scan and the only floor is catalogue-wide. That is not
// exploitable, and the reason lives in another file —
// TestEveryRendererPropagatesItsAttrs requires data-testid="probe-id" in the
// output of every registry renderer, so a renderer that rendered nothing goes
// red there. This guard's universality depends on that one.
func TestNoRendererEmitsTwoIDsOnOneElement(t *testing.T) {
	type probe struct {
		name, id, attrsID string
		fill, flags       bool
	}
	probes := []probe{
		{name: "neither"},
		{name: "attrs id only", attrsID: "probe-attrs"},
		{name: "id only", id: "probe-id"},
		{name: "both", id: "probe-id", attrsID: "probe-attrs"},
		{name: "collections filled", id: "probe-id", attrsID: "probe-attrs", fill: true},
		{name: "collections filled, flags set", id: "probe-id", attrsID: "probe-attrs", fill: true, flags: true},
	}
	identified, deepened := 0, 0
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		elements := map[string]int{}
		for _, p := range probes {
			opts := seededOpts(t, renderer, fn)
			if hasStringField(opts, "Name") {
				setOptsFields(t, renderer, opts, map[string]any{"Name": "probe-name"})
			}
			if hasStringField(opts, "ID") {
				opts.FieldByName("ID").SetString(p.id)
			}
			if attrs := opts.FieldByName("Attrs"); attrs.IsValid() {
				if id := attrs.FieldByName("ID"); id.IsValid() && id.Kind() == reflect.String {
					id.SetString(p.attrsID)
				}
			}
			if p.fill {
				fillOpts(opts, 4, p.flags)
			}
			html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
			tags := startTags(html)
			elements[p.name] = len(tags)
			for _, tag := range tags {
				switch n := idAttributes(tag); {
				case n == 1:
					identified++
				case n > 1:
					assert.Failf(t, "duplicate id attribute",
						"%s (%s) put %d id attributes on one element, so the caller's id loses to the component's: %s",
						renderer, p.name, n, tag)
				}
			}
		}
		if max(elements["collections filled"], elements["collections filled, flags set"]) > elements["both"] {
			deepened++
		}
	}
	require.Greater(t, identified, 30,
		"only %d elements carried an id across the whole catalog; the tag scan has collapsed, not the catalog", identified)
	// The stimulus floor. If filling the collections stopped reaching further
	// than the seeds do, the fifth probe has become a copy of the fourth and
	// every branch behind a collection is unscanned again - which is the state
	// this probe exists to end, and it looks identical to a clean catalogue.
	require.Greater(t, deepened, 40,
		"filling the collections rendered more elements for only %d renderers; the reflective seeding has collapsed", deepened)
}

// startTags returns the raw source of every start or self-closing tag in html.
// Quoting is tracked, so a > inside an attribute value cannot end a tag early
// and split one element's attributes across two counts - which would make this
// scan nearly right, the one thing a validity gate must not be.
//
// Go's HTML parser is deliberately not used: per spec it DROPS a duplicate
// attribute while parsing, so the evidence this test exists to find does not
// survive the parse.
func startTags(html string) []string {
	var out []string
	for i := 0; i < len(html); i++ {
		if html[i] != '<' || i+1 == len(html) || !isTagNameStart(html[i+1]) {
			continue
		}
		end := tagEnd(html, i+1)
		if end < 0 {
			break
		}
		out = append(out, html[i:end+1])
		i = end
	}
	return out
}

// tagEnd is the index of the > closing the tag whose name starts at from, or
// -1 when the markup is truncated.
func tagEnd(html string, from int) int {
	var quote byte
	for i := from; i < len(html); i++ {
		c := html[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

func isTagNameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// idAttributes counts the id attributes one start tag carries. The leading
// separator is required, so data-id= and aria-id= are not miscounted as id=.
func idAttributes(tag string) int {
	count := 0
	for i := 1; i+3 <= len(tag); i++ {
		if isTagSpace(tag[i-1]) && tag[i:i+3] == "id=" {
			count++
		}
	}
	return count
}

func isTagSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
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
