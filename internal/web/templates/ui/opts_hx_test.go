package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryHXFieldReachesTheElement walks HX's own fields, so it proves the
// emitter is complete. It cannot prove the emitter is ever *called*: an opts
// struct that declares an HX and never passes it to applyHX looks correct in
// source review and drops every request silently. ButtonOpts, IconButtonOpts,
// ToggleButtonOpts and ToggleGroupOpts all did exactly that, and three of the
// dead call sites were production actions - a project export and an
// organization export button that did nothing at all when clicked.
//
// So both halves are derived from what is installed. The HX-bearing types come
// from the package source; the renderer that carries each one to the element is
// found by reflecting over the generated renderer registry for an options tree
// that reaches that type. A hand-written table of renderers here named seven
// types owned by six other modules from ui-core's own payload, which is a
// payload every closure installs.
func TestEveryOptsHXFieldReachesTheElement(t *testing.T) {
	declared := typesCarryingHX(t)
	require.NotEmpty(t, declared, "no HX-bearing types found - the source scan is broken, not the package")

	probe := HX{Post: "/guard", Target: "#out", Swap: "none"}
	reached := map[string]bool{}
	for renderer, raw := range renderers() {
		fn := reflect.ValueOf(raw)
		opts := seededOpts(t, renderer, fn)
		carriers := setEveryHX(opts, probe, map[reflect.Type]bool{})
		if len(carriers) == 0 {
			continue
		}
		html := renderComponent(t, fn.Call([]reflect.Value{opts})[0].Interface().(templ.Component))
		if !strings.Contains(html, `hx-post="/guard"`) {
			// Not a failure on its own: a type is only unguarded if NO
			// installed renderer carries it, which the loop below decides.
			continue
		}
		for _, carrier := range carriers {
			reached[carrier] = true
		}
	}

	for _, name := range declared {
		assert.Truef(t, reached[name],
			"%s declares an HX field but no installed renderer carries it to the element, so the "+
				"caller's request is dropped", name)
	}
}

// setEveryHX sets every HX field reachable in one options value and returns the
// types it set them on. A slice of items is given exactly one element, with its
// own Label and Value filled in, because a menu or a toggle group with no items
// renders no item element for the request to reach.
//
// The walk is bounded by the types it has already entered: TreeNode declares
// []TreeNode and CommandGroup declares []CommandItem which declares its own
// children, so an unbounded walk builds an infinite value and overflows the
// stack. Re-entering a type could not find a new HX field anyway.
//
// Attrs is skipped: it is the generic bundle, and
// TestEveryHXFieldReachesTheElement already proves its emitter is complete.
// TestDedicatedHXWinsOverAttrsHX covers the two together.
func setEveryHX(value reflect.Value, probe HX, entered map[reflect.Type]bool) []string {
	if value.Kind() != reflect.Struct || entered[value.Type()] {
		return nil
	}
	entered[value.Type()] = true
	var set []string
	typ := value.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() || field.Name == "Attrs" {
			continue
		}
		target := value.Field(i)
		switch {
		case field.Name == "HX" && field.Type == reflect.TypeOf(HX{}):
			target.Set(reflect.ValueOf(probe))
			set = append(set, typ.Name())
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Struct:
			item := reflect.New(field.Type.Elem()).Elem()
			inner := setEveryHX(item, probe, entered)
			if len(inner) == 0 {
				continue
			}
			for _, name := range []string{"Label", "Value"} {
				if seed := item.FieldByName(name); seed.IsValid() && seed.Kind() == reflect.String {
					seed.SetString("probe-" + strings.ToLower(name))
				}
			}
			target.Set(reflect.Append(reflect.MakeSlice(field.Type, 0, 1), item))
			set = append(set, inner...)
		}
	}
	return set
}

// typesCarryingHX reports every exported type in this package with a top-level
// field named HX of type HX, excluding Attrs itself - Attrs is the bundle, and
// TestEveryHXFieldReachesTheElement already proves its emitter is complete.
func typesCarryingHX(t *testing.T) []string {
	t.Helper()

	pkgs, err := parser.ParseDir(token.NewFileSet(), ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var names []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				spec, ok := node.(*ast.TypeSpec)
				if !ok || !spec.Name.IsExported() || spec.Name.Name == "Attrs" {
					return true
				}
				structType, ok := spec.Type.(*ast.StructType)
				if !ok {
					return true
				}
				if structCarriesHX(structType) {
					names = append(names, spec.Name.Name)
				}
				return true
			})
		}
	}
	sort.Strings(names)
	return names
}

func structCarriesHX(structType *ast.StructType) bool {
	for _, field := range structType.Fields.List {
		ident, ok := field.Type.(*ast.Ident)
		if !ok || ident.Name != "HX" {
			continue
		}
		for _, name := range field.Names {
			if name.Name == "HX" {
				return true
			}
		}
	}
	return false
}
