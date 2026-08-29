package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
// So the type list is derived from the source rather than written down. Adding a
// struct with an HX field and no entry below fails here, which is the only way
// the guard cannot be silently skipped.
func hxBearingRenders() map[string]func(HX) templ.Component {
	return map[string]func(HX) templ.Component{
		"ButtonOpts": func(hx HX) templ.Component {
			return Button(ButtonOpts{Label: "Export", HX: hx})
		},
		"IconButtonOpts": func(hx HX) templ.Component {
			return IconButton(IconButtonOpts{Icon: IconRefresh, Label: "Reload", HX: hx})
		},
		"ToggleButtonOpts": func(hx HX) templ.Component {
			return ToggleButton(ToggleButtonOpts{Label: "Enabled", HX: hx})
		},
		"ComposerOpts": func(hx HX) templ.Component {
			return Composer(ComposerOpts{Name: "body", SubmitLabel: "Send", HX: hx})
		},
		"ConfirmActionOpts": func(hx HX) templ.Component {
			return ConfirmAction(ConfirmActionOpts{
				ID: "confirm-guard", TriggerLabel: "Delete",
				Title: "Delete this?", ConfirmLabel: "Delete", CancelLabel: "Cancel",
				HX: hx,
			})
		},
		"MenuItem": func(hx HX) templ.Component {
			return DropdownMenu(DropdownMenuOpts{
				ID: "menu-guard", Label: "Actions",
				Items: []MenuItem{{Label: "Archive", HX: hx}},
			})
		},
		"ToggleOption": func(hx HX) templ.Component {
			return ToggleGroup(ToggleGroupOpts{
				Label:   "Density",
				Options: []ToggleOption{{Value: "compact", Label: "Compact", HX: hx}},
			})
		},
	}
}

func TestEveryOptsHXFieldReachesTheElement(t *testing.T) {
	declared := typesCarryingHX(t)
	require.NotEmpty(t, declared, "no HX-bearing types found - the source scan is broken, not the package")

	renders := hxBearingRenders()
	for _, name := range declared {
		render, ok := renders[name]
		require.Truef(t, ok,
			"%s declares an HX field but has no entry in hxBearingRenders, so nothing proves the request "+
				"reaches the element - add one rather than leaving the field unguarded", name)

		html := renderComponent(t, render(HX{Post: "/guard", Target: "#out", Swap: "none"}))
		assert.Containsf(t, html, `hx-post="/guard"`,
			"%s.HX was set but no hx-post reached the rendered element, so the caller's request is dropped", name)
	}

	for name := range renders {
		assert.Containsf(t, declared, name,
			"hxBearingRenders has an entry for %s, which no longer declares an HX field - drop the entry", name)
	}
}

// A caller may set both the dedicated HX field and Attrs.HX. Both must reach the
// element, and when they name the same attribute the dedicated field has to win:
// the caller who wrote HX: on this button is naming this button's request, and
// losing to a value that arrived inside an Attrs literal is the surprise.
func TestDedicatedHXWinsOverAttrsHX(t *testing.T) {
	html := renderComponent(t, Button(ButtonOpts{
		Label: "Export",
		HX:    HX{Post: "/dedicated"},
		Attrs: Attrs{HX: HX{Post: "/generic", Target: "#out"}},
	}))

	assert.Contains(t, html, `hx-post="/dedicated"`,
		"the dedicated HX field must win the attributes it sets")
	assert.NotContains(t, html, "/generic",
		"Attrs.HX must not shadow the dedicated field")
	assert.Contains(t, html, `hx-target="#out"`,
		"Attrs.HX must still supply the attributes the dedicated field leaves unset")
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
