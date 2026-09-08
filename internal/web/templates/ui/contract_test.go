package ui

import (
	"github.com/a-h/templ"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every exported renderer takes exactly one options struct named after it, and
// that struct declares a NAMED field Attrs of type Attrs - not an embed, so
// nothing is promoted and a caller writes o.Attrs.ID. The uniformity is the point:
// a caller never has to remember an argument order, adding an option is not a
// signature change for every existing call site, and Attrs is the single place
// component-owned semantics are protected from callers.
func TestEveryExportedRendererTakesOneOptionsStruct(t *testing.T) {
	fset := token.NewFileSet()
	// Every non-test Go file, for the reason exportedRendererNames records: a
	// renderer hand-written in a plain .go file takes whatever arguments it
	// likes, and while this scan globbed *_templ.go it was exempt from the one
	// contract that makes the catalogue uniform.
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
	}
	require.Greater(t, len(files), 100,
		"only %d non-test .go files found; the scan is looking in the wrong place", len(files))

	checked := 0
	for _, path := range files {
		parsed, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() {
				continue
			}
			// A renderer is a function returning templ.Component.
			if !returnsTemplComponent(fn) {
				continue
			}
			checked++
			name := fn.Name.Name
			params := fn.Type.Params.List
			require.Len(t, params, 1,
				"%s must take exactly one options struct, not %d parameters", name, len(params))
			require.Len(t, params[0].Names, 1, "%s must take a single named parameter", name)

			ident, ok := params[0].Type.(*ast.Ident)
			require.True(t, ok, "%s takes %T rather than a named options struct", name, params[0].Type)
			assert.Equal(t, name+"Opts", ident.Name,
				"%s must take %sOpts so the option type is discoverable from the renderer name", name, name)
		}
	}
	require.Greater(t, checked, 20, "the scan found suspiciously few renderers")
}

// Attrs deliberately has no arbitrary attribute map: with one, any caller could
// set role, aria-*, tabindex or type and silently change what a component means
// to assistive technology.
func TestAttrsHasNoArbitraryAttributeEscapeHatch(t *testing.T) {
	typ := reflect.TypeOf(Attrs{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		if field.Type.Kind() != reflect.Map {
			continue
		}
		assert.Contains(t, []string{"Data"}, field.Name,
			"Attrs.%s is a map that could carry arbitrary attributes", field.Name)
	}
	for _, forbidden := range []string{"Attributes", "Extra", "Attrs", "Role", "AriaLabel", "TabIndex", "Type"} {
		_, found := typ.FieldByName(forbidden)
		assert.False(t, found, "Attrs must not expose %s: components own their semantics", forbidden)
	}
}

// An unset or unrecognised Kind renders neutral rather than an uncoloured
// element, and never silently reads as the brand default.
func TestNormalizeKindClosesTheEnum(t *testing.T) {
	for _, kind := range Kinds {
		assert.Equal(t, kind, NormalizeKind(kind))
		assert.True(t, kind.Valid())
	}
	for _, bogus := range []Kind{"", "primary", "Danger", "info "} {
		assert.Equal(t, KindNeutral, NormalizeKind(bogus), "%q must normalize to neutral", bogus)
		assert.False(t, bogus.Valid())
	}
}

func returnsTemplComponent(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	sel, ok := fn.Type.Results.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "templ" && sel.Sel.Name == "Component"
}

// A declared TestID must reach the DOM, and a declared Class must reach the
// component's ROOT element. Every renderer accepts Attrs, so a renderer that
// ignores them looks configurable while silently dropping the caller's class and
// test id - which is how Tooltip shipped with an Attrs field nothing read.
//
// This is the only Attrs contract test. There used to be a second one over a
// hand-kept list of 44 options structs, guarded by a test whose comment claimed
// the list was tied to the package and which only checked that the list was
// non-empty, free of duplicates, and full of names ending in "Opts" - so three
// quarters of the package was exempt and nothing said so. Reflection over the
// generated renderers table covers all of them, including the field's type.
func TestEveryRendererPropagatesItsAttrs(t *testing.T) {
	table := renderers()
	// The AST scan is the authority: a renderer whose module declares no
	// runtime.ui entry would be missing from the generated table, and so
	// silently exempt from this contract.
	for _, name := range exportedRendererNames(t) {
		require.Contains(t, table, name,
			"renderer %s is not in the generated renderers table, so nothing checks it; "+
				"its module declares no runtime.ui entry naming it", name)
	}

	for name, raw := range table {
		fn := reflect.ValueOf(raw)
		t.Run(name, func(t *testing.T) {
			// Some renderers correctly render nothing for a zero value: a
			// single-page pager and an unnamed icon are both "no output" by
			// design, so they need the minimum input that makes them render.
			opts := seededOpts(t, name, fn)
			attrs := opts.FieldByName("Attrs")
			require.True(t, attrs.IsValid(), "%sOpts has no Attrs field", name)
			require.Equal(t, reflect.TypeOf(Attrs{}), attrs.Type(),
				"%sOpts.Attrs must be ui.Attrs, or callers set id/class/test-id/Alpine/HTMX differently here", name)
			attrs.FieldByName("TestID").SetString("probe-id")
			attrs.FieldByName("Class").SetString("probe-class")

			out := fn.Call([]reflect.Value{opts})
			component, ok := out[0].Interface().(templ.Component)
			require.True(t, ok, "renderer must return a templ.Component")

			html := renderComponent(t, component)
			assert.Contains(t, html, `data-testid="probe-id"`,
				"%s ignores Attrs.TestID, so no test can target it", name)
			// On the element the caller actually addressed, in a class
			// attribute. Searching the whole document passes when the string
			// merely appears somewhere - inside a data-* value, or as text -
			// which is not the contract. TestID and Class come from one Attrs
			// and are flattened into one attribute map, so they land on the same
			// element by construction: asserting them together is what makes the
			// class assertion mean "reached the element" rather than "reached
			// the output".
			assert.Contains(t, classOfProbedElement(t, html), "probe-class",
				"%s drops Attrs.Class from the element that carries Attrs.TestID", name)
		})
	}
}

// classOfProbedElement returns the class attribute of the element carrying the
// probe test id, and refuses an element carrying two.
//
// Two class attributes on one tag is invalid HTML and the browser keeps the
// FIRST, so a component that writes its own class beside a spread map loses its
// own styling to any caller who sets Attrs.Class - which is exactly what
// Checkbox and Switch did to their `h-4 w-4`. The failure is invisible in a
// substring search over the whole document, because both strings are present.
func classOfProbedElement(t *testing.T, html string) string {
	t.Helper()
	at := strings.Index(html, `data-testid="probe-id"`)
	require.GreaterOrEqual(t, at, 0, "the probe test id is not in the output")
	open := strings.LastIndex(html[:at], "<")
	require.GreaterOrEqual(t, open, 0, "the probe test id is not inside a tag")
	end := strings.Index(html[open:], ">")
	require.Greater(t, end, 0, "unterminated tag around the probe test id")
	tag := html[open : open+end]
	matches := probedClassAttr.FindAllStringSubmatch(tag, -1)
	require.Len(t, matches, 1,
		"probed element must carry exactly one class attribute, got %d: %s", len(matches), tag)
	return matches[0][1]
}

var probedClassAttr = regexp.MustCompile(`\sclass="([^"]*)"`)

// rendererSeeds supplies the minimum options, BY FIELD NAME, for renderers
// whose zero value legitimately produces no output.
//
// The keys are strings and the fields are set reflectively because ui-core owns
// none of these renderers. Naming SelectionBarOpts here put a type
// ggg/component/selection-bar owns inside ui-core's own test payload, and a
// closure that installs ui-core without selection-bar - which `minimal` does -
// wrote a tree where `go test ./...` did not compile. A string key for an
// uninstalled renderer is simply never consulted.
func rendererSeeds() map[string]map[string]any {
	return map[string]map[string]any{
		"Pagination": {"Page": 1, "TotalPages": 3, "BaseURL": "/x", "Target": "#t"},
		"Icon":       {"Name": IconLogo},
		// A renderer that draws a control or a menu needs the label it is
		// addressed by before any request can reach an element.
		"Button":       {"Label": "Export"},
		"IconButton":   {"Icon": IconRefresh, "Label": "Reload"},
		"ToggleButton": {"Label": "Enabled"},
		"Composer":     {"Name": "body", "SubmitLabel": "Send"},
		"ConfirmAction": {
			"ID": "confirm-guard", "TriggerLabel": "Delete",
			"Title": "Delete this?", "ConfirmLabel": "Delete", "CancelLabel": "Cancel",
		},
		"DropdownMenu": {"ID": "menu-guard", "Label": "Actions"},
		"ToggleGroup":  {"Label": "Density"},
		// Both ends of a keyset sequence render nothing, which is the point.
		"CursorPagination": {"NextURL": "/x?after=1", "Target": "#t"},
		// An empty selection has no bulk actions to offer.
		"SelectionBar": {"Count": 2, "CountLabel": "2 selected"},
		// No request context in a unit render, so the field has no token to
		// draw and correctly renders nothing.
		"CSRFField": {"Token": "probe-token"},
	}
}

// seededOpts builds the options value one renderer is exercised with: its zero
// value, plus any declared seed, set by field name.
func seededOpts(t *testing.T, name string, renderer reflect.Value) reflect.Value {
	t.Helper()
	require.Equal(t, 1, renderer.Type().NumIn(), "%s must take exactly one options struct", name)
	opts := reflect.New(renderer.Type().In(0)).Elem()
	setOptsFields(t, name, opts, rendererSeeds()[name])
	return opts
}

// setOptsFields sets options fields by name. A stale seed - a field the
// renderer no longer declares, or one whose type the declared value cannot
// become - fails rather than being skipped, because a silently ignored seed
// turns a rendering assertion into an assertion about empty output.
func setOptsFields(t *testing.T, name string, opts reflect.Value, fields map[string]any) {
	t.Helper()
	for field, value := range fields {
		target := opts.FieldByName(field)
		require.Truef(t, target.IsValid(),
			"%sOpts has no %s field, so the declared value is stale", name, field)
		declared := reflect.ValueOf(value)
		require.Truef(t, declared.Type().ConvertibleTo(target.Type()),
			"%sOpts.%s is %s, which a %s cannot become", name, field, target.Type(), declared.Type())
		target.Set(declared.Convert(target.Type()))
	}
}

// fillOpts populates every zero-valued field an options struct exposes, so a
// branch that only runs when a collection is non-empty actually runs.
//
// rendererSeeds is a 13-entry hand table, and it decides which branches of 174
// renderers ever render. That makes it the STIMULUS version of a
// declaration-filtered population: DropdownMenu is seeded with an ID and a
// Label but not with Items, so the loop over its items never executed under
// any probe, and a span carrying two id attributes inside that loop rendered
// on every production menu in the catalogue while both packages stayed green.
//
// So the collections are filled reflectively rather than named. Only zero
// values are written, which leaves every seed and every probe value intact,
// and the descent stops at three levels because the shapes here are data
// structs and a cycle would otherwise be unbounded. Interfaces, functions and
// channels are left nil: a templ.Component field is a caller's children, and
// synthesising one would assert about this test's markup rather than the
// renderer's.
func fillOpts(opts reflect.Value, depth int, flag bool) {
	if depth <= 0 || !opts.IsValid() {
		return
	}
	switch opts.Kind() {
	case reflect.Struct:
		for i := range opts.NumField() {
			field := opts.Field(i)
			if !field.CanSet() {
				continue
			}
			// Attrs is the probe's own axis; filling it would overwrite the
			// id being tested and put a class on every element.
			if opts.Type().Field(i).Name == "Attrs" {
				continue
			}
			fillOpts(field, depth-1, flag)
		}
	case reflect.Slice:
		if !opts.IsNil() && opts.Len() > 0 {
			for i := range opts.Len() {
				fillOpts(opts.Index(i), depth-1, flag)
			}
			return
		}
		// Two elements, not one: a separator, a divider or an "and N more"
		// branch commonly renders only between items.
		filled := reflect.MakeSlice(opts.Type(), 2, 2)
		for i := range 2 {
			// The two elements disagree about every boolean, so a branch that
			// runs only for a flagged item and a branch that runs only for an
			// unflagged one are both rendered in one pass. A separator is a
			// bool on MenuItem, and two separators would have hidden the
			// acting branch as surely as none hid the separator.
			fillOpts(filled.Index(i), depth-1, i == 1 != flag)
		}
		opts.Set(filled)
	case reflect.Map:
		if !opts.IsNil() && opts.Len() > 0 {
			return
		}
		key := reflect.New(opts.Type().Key()).Elem()
		fillOpts(key, depth-1, flag)
		value := reflect.New(opts.Type().Elem()).Elem()
		fillOpts(value, depth-1, flag)
		filled := reflect.MakeMap(opts.Type())
		filled.SetMapIndex(key, value)
		opts.Set(filled)
	case reflect.Pointer:
		if !opts.IsNil() {
			fillOpts(opts.Elem(), depth-1, flag)
			return
		}
		allocated := reflect.New(opts.Type().Elem())
		fillOpts(allocated.Elem(), depth-1, flag)
		opts.Set(allocated)
	case reflect.Bool:
		// Only ever set, never cleared: a seed that declared a flag true keeps
		// it, and the flag axis is covered by running the probe both ways.
		if !opts.Bool() && flag {
			opts.SetBool(true)
		}
	case reflect.String:
		if opts.Len() == 0 {
			opts.SetString("probe")
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if opts.Int() == 0 {
			opts.SetInt(1)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if opts.Uint() == 0 {
			opts.SetUint(1)
		}
	case reflect.Float32, reflect.Float64:
		if opts.Float() == 0 {
			opts.SetFloat(1)
		}
	default:
	}
}

// renderers is every renderer this project installed, keyed by symbol.
//
// It is the generated registry - rendered from the same runtime.ui declarations
// the component reference and the gallery are rendered from - plus CSRFField,
// which ui-core owns itself and which declares no gallery component because it
// renders a hidden input a reader cannot look at. It used to be a hand-written
// table of 175 symbols owned by 144 other modules; a payload that names every
// component only compiles in a closure containing every component.
func renderers() map[string]any {
	table := Renderers()
	table["CSRFField"] = CSRFField
	return table
}

// typeOf is reflect.TypeOf under a shorter name, for tables that compare a
// declared field's type against the type a caller passes.
func typeOf(v any) reflect.Type { return reflect.TypeOf(v) }

// exportedRendererNames returns every exported function in this package that
// returns a templ.Component.
//
// The population is the package's Go files, not its generated templ output.
// Globbing *_templ.go was right about one thing - a regexp over the .templ
// sources is how the counts 172 and 175 came to disagree, so the AST of
// compiled Go is the honest reading - and wrong about the file kind: a
// renderer hand-written in a plain .go file compiles, exports and renders
// exactly like a generated one, and was in NEITHER population. One such file
// voided four contracts at once with a green build: it took no options
// struct, ignored Attrs entirely, put two ids on one element and rendered two
// prohibited utilities. Zero exist today; the package already holds eight
// hand-written .go files and CSRFField is already a hand-added registry
// entry, so "a renderer the registry does not know about" is a shape this
// package already has.
//
// Test files are excluded: a renderer declared in a _test.go is not installed
// and cannot be reached by a page.
func exportedRendererNames(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var out []string
	scanned := 0
	for _, entry := range entries {
		path := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, parseErr)
		scanned++
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || !returnsTemplComponent(fn) {
				continue
			}
			out = append(out, fn.Name.Name)
		}
	}
	require.Greater(t, scanned, 100,
		"only %d non-test .go files parsed; the scan is looking in the wrong place", scanned)
	require.NotEmpty(t, out)
	return out
}

// A control that acts is a button and a control that navigates is a link, and
// the two must not be one renderer: Href on an acting control produces
// <a href=""> or a button that navigates, and either way a middle click, a
// Space press or a right-click "open in new tab" does the wrong thing.
//
// Derived over every installed renderer rather than asserted against three
// options structs three other modules own, which is what button-link_test.go
// used to do from inside a payload it does not own.
func TestActingRenderersCarryNoDestination(t *testing.T) {
	acting := 0
	for name, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, name, fn)
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		if !strings.Contains(html, "<button") || strings.Contains(html, "<a ") {
			continue
		}
		acting++
		_, hasHref := opts.Type().FieldByName("Href")
		assert.Falsef(t, hasHref,
			"%sOpts accepts Href but renders a button: it acts, it does not navigate", name)
	}
	require.Greater(t, acting, 3,
		"only %d acting renderers were derived; the filter has collapsed, not the catalog", acting)
}

// MenuItem and Column are data contracts ui-core owns and several renderers in
// other modules read: DropdownMenu, ContextMenu, RowActions and Kanban all read
// MenuItem, and Table, DataTable, DataGrid and TreeGrid all read Column. A
// consumer that needed its own field would fork the contract and break the
// others, so the SHAPE is fixed here, once.
//
// Whether a renderer honours a declared field is that renderer's contract and
// is asserted in its own payload - Column.Width lives in column-header_test.go
// and tree-grid_test.go. Rendering both from here named two components ui-core
// does not own from the payload every closure installs.
func TestSharedDataContractShapes(t *testing.T) {
	menu := reflect.TypeOf(MenuItem{})
	for name, want := range map[string]reflect.Kind{
		"Label": reflect.String, "Href": reflect.String,
		"Icon": reflect.String, "Kind": reflect.String,
		"Disabled": reflect.Bool, "Separator": reflect.Bool,
		"Confirm": reflect.String,
	} {
		field, ok := menu.FieldByName(name)
		require.True(t, ok, "MenuItem has no %s field", name)
		assert.Equal(t, want, field.Type.Kind(), "MenuItem.%s has the wrong kind", name)
	}
	iconField, _ := menu.FieldByName("Icon")
	assert.Equal(t, reflect.TypeOf(IconName("")), iconField.Type,
		"MenuItem.Icon must be the typed icon name, not a free string")
	hx, ok := menu.FieldByName("HX")
	require.True(t, ok, "a menu item that issues a request needs HX")
	assert.Equal(t, reflect.TypeOf(HX{}), hx.Type)

	col := reflect.TypeOf(Column{})
	hide, ok := col.FieldByName("HideBelow")
	require.True(t, ok, "a column that cannot be hidden forces horizontal scroll on small screens")
	assert.Equal(t, reflect.TypeOf(Breakpoint("")), hide.Type)
	align, _ := col.FieldByName("Align")
	assert.Equal(t, reflect.TypeOf(Align("")), align.Type)

	width, ok := col.FieldByName("Width")
	require.True(t, ok, "a column with no width cannot stop a timestamp column wrapping")
	assert.Equal(t, reflect.String, width.Type.Kind(),
		"Width is a CSS length, so it is a string rather than a pixel count")
}

// A form whose submission the CSRF check applies to must carry the token, and
// the only way a component can know is to render CSRFField. Production templates
// are covered by the design-system guard, but ui/ is where forms are legitimately
// built, so nothing watched these three renderers - and two of them shipped the
// exact defect that guard exists to prevent: Composer submitted as a GET to the
// current URL because its verb lived in an htmx attribute, and Questionnaire
// declared method="post" with no field, so a plain submit answered 403.
//
// Scanning source rather than rendering is deliberate: a rendered form only
// carries the token when a token is in the context, so a rendering test would
// pass on an empty context for the wrong reason.
func TestEveryUnsafeFormInThePackageRendersTheToken(t *testing.T) {
	entries, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no templ sources found, so this test proves nothing")
	}

	// form.templ renders the field through formCarriesCSRF, and alert-dialog's
	// form is method="dialog" - a platform close, never a request. Both are
	// stated rather than pattern-matched, because each is a real decision.
	exempt := map[string]string{
		"form.templ":         "renders CSRFField itself, gated on the derived method",
		"alert-dialog.templ": `method="dialog" closes the dialog and issues no request`,
	}

	for _, path := range entries {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		// Comment lines are stripped first. markdown-editor.templ documents the
		// caller's obligation by showing the <form id=... action=...> the page
		// must render, and a scan that counted prose would have reported a
		// component with no form element at all as missing its token.
		source := withoutCommentLines(string(body))
		if !strings.Contains(source, "<form") {
			continue
		}
		if reason, ok := exempt[filepath.Base(path)]; ok {
			assert.NotEmpty(t, reason)
			continue
		}

		// A form is unsafe if it declares a mutating method or carries a
		// mutating htmx verb. Either one means a browser submit gets checked.
		unsafe := strings.Contains(source, `method="post"`) ||
			strings.Contains(source, "composerHX") ||
			strings.Contains(source, "hx-post") ||
			strings.Contains(source, "hx-put") ||
			strings.Contains(source, "hx-patch") ||
			strings.Contains(source, "hx-delete") ||
			strings.Contains(source, "HX.Post") ||
			strings.Contains(source, "hxMutatingURL")
		if !unsafe {
			continue
		}

		assert.Containsf(t, source, "CSRFField",
			"%s renders a form whose submission is checked but never renders CSRFField, so its no-script path answers 403", path)
	}
}

// withoutCommentLines drops whole-line comments so a scan reads markup rather
// than prose. Anything a comment says about a form is documentation, not an
// element the browser will ever submit.
func withoutCommentLines(source string) string {
	lines := strings.Split(source, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
