package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A class a renderer emits and no stylesheet declares is a dead class: a
// typed, reachable, declared option that renders a control indistinguishable
// from the default, or a shape with no colour in it.
//
// Three matrix guards next door assert that for the size and kind axes, and
// all three select their population with probe.FieldByName("Size") — a
// TOP-LEVEL field of type ui.Size. So an axis declared one level down is
// invisible to them: giving BadgeOpts a nested `Sizing struct{ Size Size }`
// and rendering `badge-xxl` for SizeLG — a class input.css never declares —
// left all three green, and the design-system scan too. That is the same
// nesting blind spot control-id_test.go documents for PanelData.ID and
// SlideData.ID; the size filter reached one level less deep than the id
// derivation beside it.
//
// So this asks the question the other way round. The population is not the
// renderers whose options declare an axis: it is every installed renderer,
// rendered under every value of every closed enum its options expose AT ANY
// DEPTH, and the assertion is about the classes that actually come out. No
// field name, no field type and no depth appears in the filter, so an axis
// moved, renamed or nested cannot leave the population.
//
// Both stylesheets answer, because the design system has two homes and a class
// is styled if either declares it:
//
//   - input.css selects on the component classes this project owns;
//   - static/app.css is what Tailwind compiled, so every utility the build
//     actually emitted is in it, escaped variants and all.
//
// That pairing is what makes the rule need no allow-list of utilities to
// ignore — and it could not have one, since Tailwind's set is open. It also
// makes the rule catch the failure mode a source scan cannot see: Tailwind
// decides what to emit by scanning source TEXT, so a class assembled by
// concatenation is a class the build never compiles. Measured, at HEAD, on
// the first run: StatusDot and ChartLegend composed `"bg-" + string(kind)`,
// and bg-info and bg-warn were written literally nowhere in the tree, so both
// rendered a shape with no fill. bg-brand, bg-success, bg-danger and
// bg-neutral worked only because unrelated components happen to spell them
// out. ui.KindFillClass now spells all six.
//
// One assumption, stated because a stale build would make this fail for the
// wrong reason: `ggg check` runs the Tailwind build before the suite, so the
// app.css read here is the one the current source produces. The neighbouring
// TestBuiltStylesheetCarriesKindVariables rests on the same thing.
func TestEveryClassARendererEmitsIsStyled(t *testing.T) {
	authored := declaredClassSelectors(t, readInputCSSFromUI(t))
	require.Greater(t, len(authored), 80,
		"only %d class selectors extracted from input.css; the stylesheet reader has collapsed and this test would "+
			"report every component class as unstyled", len(authored))
	built := declaredClassSelectors(t, readBuiltCSSFromUI(t))
	require.Greater(t, len(built), 300,
		"only %d class selectors extracted from static/app.css; the built sheet is unreadable and every utility "+
			"would read as unstyled", len(built))

	rendered := renderedClassesByRenderer(t)
	require.Greater(t, len(rendered), 100,
		"only %d renderers produced any class at all; the render sweep has collapsed, not the catalogue", len(rendered))

	checked, componentClasses := 0, 0
	for renderer, classes := range rendered {
		for _, class := range classes {
			// The rendered attribute value is HTML-escaped; Tailwind's
			// arbitrary variants carry `&` and `>`.
			class = html.UnescapeString(class)
			// A variant prefix (`hover:`, `md:`) is part of the compiled
			// selector but not of a component class name, so both readings are
			// offered.
			bare := class
			if at := strings.LastIndexByte(bare, ':'); at >= 0 {
				bare = bare[at+1:]
			}
			checked++
			if authored[bare] || authored[class] {
				componentClasses++
				continue
			}
			assert.Truef(t, built[class] || built[bare],
				"%s renders %q and neither input.css nor the built static/app.css declares it.\n"+
					"fix: if it is a component class, give it a rule in @layer components — an option that "+
					"renders an undeclared class renders a control byte-identical to the default. If it is a "+
					"Tailwind utility, it was never compiled: check that it is written as a literal rather than "+
					"assembled from pieces, because the build decides what to emit by scanning source text.",
				renderer, class)
		}
	}
	require.Greater(t, checked, 500,
		"only %d class tokens collected across the catalogue; the class scraper has collapsed", checked)
	require.Greater(t, componentClasses, 50,
		"only %d rendered classes were recognised as this project's own component classes; the input.css "+
			"predicate has stopped matching and the rule is now carried entirely by the built sheet", componentClasses)
}

// renderedClassesByRenderer renders every installed renderer under every probe
// and returns the distinct class tokens each one emitted.
//
// The probes are the seeded options, the reflectively filled options both ways
// (so a branch behind a collection or a flag renders), and then one pass per
// declared value of every closed enum in the package, applied to every field
// of that type at any depth.
func renderedClassesByRenderer(t *testing.T) map[string][]string {
	t.Helper()
	enums := declaredEnumValues(t)
	require.Greater(t, len(enums), 8,
		"only %d closed enums found in this package; the axis sweep would be a sweep over nothing", len(enums))

	out := map[string]map[string]struct{}{}
	record := func(renderer, html string) {
		for _, tag := range startTags(html) {
			for _, match := range probedClassAttr.FindAllStringSubmatch(tag, -1) {
				for _, class := range strings.Fields(match[1]) {
					if out[renderer] == nil {
						out[renderer] = map[string]struct{}{}
					}
					out[renderer][class] = struct{}{}
				}
			}
		}
	}

	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		record(renderer, renderProbe(t, renderer, fn, nil))
		for _, flag := range []bool{false, true} {
			record(renderer, renderProbe(t, renderer, fn, func(opts reflect.Value) {
				fillOpts(opts, 4, flag)
			}))
		}
		for typeName, values := range enums {
			for _, value := range values {
				record(renderer, renderProbe(t, renderer, fn, func(opts reflect.Value) {
					fillOpts(opts, 4, false)
					setEnumFields(opts, typeName, value, 4)
				}))
			}
		}
	}

	flattened := make(map[string][]string, len(out))
	for renderer, classes := range out {
		list := make([]string, 0, len(classes))
		for class := range classes {
			list = append(list, class)
		}
		sort.Strings(list)
		flattened[renderer] = list
	}
	return flattened
}

// renderProbe renders one renderer from its seeded options, after mutate has
// had its say.
func renderProbe(t *testing.T, renderer string, fn reflect.Value, mutate func(reflect.Value)) string {
	t.Helper()
	opts := seededOpts(t, renderer, fn)
	if mutate != nil {
		mutate(opts)
	}
	return renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
}

// setEnumFields sets every field of the named string type to value, at any
// depth, so an axis declared on a nested data struct is exercised exactly like
// one declared on the options root.
func setEnumFields(value reflect.Value, typeName, enum string, depth int) {
	if depth <= 0 || !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := range value.NumField() {
			field := value.Field(i)
			if !field.CanSet() {
				continue
			}
			if field.Kind() == reflect.String && field.Type().Name() == typeName {
				field.SetString(enum)
				continue
			}
			setEnumFields(field, typeName, enum, depth-1)
		}
	case reflect.Slice:
		for i := range value.Len() {
			setEnumFields(value.Index(i), typeName, enum, depth-1)
		}
	case reflect.Pointer:
		if !value.IsNil() {
			setEnumFields(value.Elem(), typeName, enum, depth-1)
		}
	default:
	}
}

// declaredEnumValues maps each closed string enum in this package to its
// declared constant values.
//
// Derived from the declarations, the same way enums_test.go derives the enum
// population: a closed enum is an exported string type with constants, and its
// values are the literals those constants carry. Nothing is named here, so a
// new axis joins the sweep by existing.
func declaredEnumValues(t *testing.T) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	stringTypes := map[string]bool{}
	values := map[string][]string{}
	for _, entry := range entries {
		path := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, parseErr)
		for _, d := range file.Decls {
			decl, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range decl.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					if id, isIdent := sp.Type.(*ast.Ident); isIdent && id.Name == "string" && sp.Name.IsExported() {
						stringTypes[sp.Name.Name] = true
					}
				case *ast.ValueSpec:
					if decl.Tok != token.CONST {
						continue
					}
					id, isIdent := sp.Type.(*ast.Ident)
					if !isIdent {
						continue
					}
					for _, v := range sp.Values {
						lit, isLit := v.(*ast.BasicLit)
						if !isLit || lit.Kind != token.STRING {
							continue
						}
						values[id.Name] = append(values[id.Name], strings.Trim(lit.Value, `"`))
					}
				}
			}
		}
	}
	out := map[string][]string{}
	for name, declared := range values {
		if stringTypes[name] {
			out[name] = declared
		}
	}
	return out
}

// declaredClassSelectors is every class the stylesheet selects on.
//
// Comments are stripped first: input.css explains the kind matrix in prose
// that names `banner-brand` and `banner-success`, so a scan that read comments
// would let a dead class be declared by mentioning it. Only the text between
// the previous rule boundary and an opening brace is read, so a class name
// appearing inside a declaration value cannot count either.
func declaredClassSelectors(t *testing.T, css string) map[string]bool {
	t.Helper()
	stripped := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, " ")
	// Backslash escapes are part of the name: Tailwind compiles `md:table-cell`
	// to the selector `.md\:table-cell`, and unescaping is what makes the two
	// stylesheets comparable to one class list.
	className := regexp.MustCompile(`\.((?:[a-zA-Z0-9_-]|\\.)+)`)
	unescape := regexp.MustCompile(`\\(.)`)
	out := map[string]bool{}
	start := 0
	for i, b := range []byte(stripped) {
		switch b {
		case '{', '}', ';':
			if b == '{' {
				for _, match := range className.FindAllStringSubmatch(stripped[start:i], -1) {
					out[unescape.ReplaceAllString(match[1], "$1")] = true
				}
			}
			start = i + 1
		}
	}
	require.NotEmpty(t, out, "input.css selects on no class at all")
	return out
}

// readInputCSSFromUI reads the token and component source from this package.
func readInputCSSFromUI(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "input.css"))
	require.NoError(t, err)
	require.Greater(t, len(source), 5000, "input.css read short; the stylesheet predicate would accept everything")
	return string(source)
}

// readBuiltCSSFromUI reads the compiled stylesheet the browser actually loads.
func readBuiltCSSFromUI(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "static", "app.css"))
	require.NoError(t, err)
	require.Greater(t, len(source), 20000, "static/app.css read short; the built-sheet predicate would accept nothing")
	return string(source)
}
