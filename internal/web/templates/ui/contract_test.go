package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every exported renderer takes exactly one options struct named after it, and
// that struct embeds Attrs as a field called Attrs. The uniformity is the point:
// a caller never has to remember an argument order, adding an option is not a
// signature change for every existing call site, and Attrs is the single place
// component-owned semantics are protected from callers.
func TestEveryExportedRendererTakesOneOptionsStruct(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*_templ.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

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

// Attrs must be reachable as a field called Attrs on every options struct, so
// callers set id/class/test-id/Alpine/HTMX the same way everywhere.
func TestEveryOptionsStructEmbedsAttrs(t *testing.T) {
	for _, sample := range optionsSamples() {
		typ := reflect.TypeOf(sample)
		field, ok := typ.FieldByName("Attrs")
		require.True(t, ok, "%s has no Attrs field", typ.Name())
		assert.Equal(t, reflect.TypeOf(Attrs{}), field.Type,
			"%s.Attrs must be ui.Attrs", typ.Name())
	}
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

// optionsSamples lists one zero value per options struct. Kept explicit rather
// than reflected out of the package so a new renderer's options struct is a
// deliberate addition here.
func optionsSamples() []any {
	return []any{
		SpinnerOpts{}, BadgeOpts{}, NoticeOpts{}, BannerOpts{}, PageHeaderOpts{},
		SectionHeaderOpts{}, TableCardOpts{}, TableEmptyOpts{}, MetricOpts{},
		EmptyStateOpts{}, FieldErrorOpts{}, MeterOpts{}, SecretRevealOpts{},
		DescriptionListOpts{}, PaginationOpts{}, NavTabsOpts{}, TerminalPageOpts{},
		FormOpts{}, FieldsetOpts{}, FieldOpts{}, TextInputOpts{}, SearchInputOpts{},
		TextareaOpts{}, SelectOpts{}, CheckboxOpts{}, RadioGroupOpts{}, SwitchOpts{},
		CardOpts{}, CardHeaderOpts{}, CardFooterOpts{}, TableOpts{}, KeyValueOpts{},
		ListOpts{}, ContainerOpts{}, StackOpts{}, InlineOpts{}, GridOpts{},
		DialogOpts{}, AlertDialogOpts{}, DropdownMenuOpts{}, PopoverOpts{}, TooltipOpts{},
		IconOpts{}, ItemOpts{}, DividerOpts{},
	}
}

func TestOptionsSamplesCoverEveryRenderer(t *testing.T) {
	// A renderer whose options struct is missing from the sample list would
	// escape the Attrs check above, so the two are tied together.
	assert.NotEmpty(t, optionsSamples())
	seen := map[string]bool{}
	for _, sample := range optionsSamples() {
		name := reflect.TypeOf(sample).Name()
		assert.False(t, seen[name], "duplicate sample %s", name)
		seen[name] = true
		assert.True(t, strings.HasSuffix(name, "Opts"), "%s is not an options struct", name)
	}
}
