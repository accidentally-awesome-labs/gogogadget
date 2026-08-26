package modkit

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"
)

// referenceFixture is a two-component catalog: one component declares the full
// documented interface, the other declares nothing optional. Both cases matter,
// because the emitter has to render the first faithfully and leave the second
// out of the literal entirely.
func referenceFixture() (Lock, []Manifest) {
	mods := []Manifest{
		{
			ID: "element/ui-core", Kind: ModuleElement, Name: "ui-core", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{UI: []UIContribution{
				{Name: "badge", Family: GalleryFeedback},
				{
					Name:      "dialog",
					Family:    GalleryOverlays,
					Engine:    "alpine",
					Alpine:    "uiDialog",
					Signature: "templ Dialog(o DialogOpts)",
					Summary:   "A modal dialog that owns focus while it is open.",
					Guidance:  "Use for a decision that blocks the page; it refuses to render without a title because a dialog with no accessible name is unannounceable.",
					Keyboard:  "Escape closes; Tab cycles inside the dialog; focus returns to the trigger.",
					States:    []string{"default", "error"},
				},
			}},
		},
		{
			ID: "component/data-grid", Kind: ModuleComponent, Name: "data-grid", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{UI: []UIContribution{
				{
					Name:      "data-grid",
					Family:    GalleryAdvanced,
					Signature: "templ DataGrid(o DataGridOpts)",
					Keyboard:  "Arrow keys move the focused cell; Home and End jump to row edges.",
				},
			}},
		},
	}
	lock := Lock{
		Order:   []string{"element/ui-core", "component/data-grid"},
		Modules: []LockedModule{{ID: "element/ui-core"}, {ID: "component/data-grid"}},
	}
	return lock, mods
}

// The reference is the only place a gallery page can learn a component's exact
// signature, purpose and keyboard contract. If the emitted file did not parse,
// or dropped a declared field, the detail page would either fail to build or
// silently publish an incomplete interface.
func TestUIReferenceRegistryEmitsDeclaredInterface(t *testing.T) {
	lock, mods := referenceFixture()
	f, err := emitUIReferenceRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if f.Path != "internal/web/templates/ui/reference_gen.go" {
		t.Fatalf("path = %s", f.Path)
	}
	if !strings.Contains(f.Content, "package ui") {
		t.Fatalf("reference registry is not in package ui:\n%s", f.Content)
	}
	if !strings.Contains(f.Content, indexSHA(lock)) {
		t.Fatalf("reference registry does not carry the lock index SHA")
	}
	for _, want := range []string{
		`Name: "dialog"`, `Family: "overlays"`, `Module: "element/ui-core"`,
		`Signature: "templ Dialog(o DialogOpts)"`,
		`Summary: "A modal dialog that owns focus while it is open."`,
		`Guidance: "Use for a decision`,
		`Keyboard: "Escape closes;`,
		`States: []string{"default", "error"}`,
	} {
		if !strings.Contains(f.Content, want) {
			t.Fatalf("reference registry missing %s:\n%s", want, f.Content)
		}
	}
}

// The generated file is compiled by the ui package, so a malformed declaration
// is a build break for the whole application. Parsing it here and asserting the
// exact exported shape catches that inside the transaction instead.
func TestUIReferenceRegistryDeclaresConsumedAPI(t *testing.T) {
	lock, mods := referenceFixture()
	f, err := emitUIReferenceRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "reference_gen.go", f.Content, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("generated reference does not parse: %v\n%s", err, f.Content)
	}
	if file.Name.Name != "ui" {
		t.Fatalf("package = %s, want ui", file.Name.Name)
	}

	fields := map[string]string{}
	var registry, lookup bool
	for _, declaration := range file.Decls {
		switch d := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					structType, ok := s.Type.(*ast.StructType)
					if !ok || s.Name.Name != "Reference" {
						continue
					}
					for _, field := range structType.Fields.List {
						for _, name := range field.Names {
							fields[name.Name] = types(field.Type)
						}
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Name == "ReferenceRegistry" {
							registry = true
						}
					}
				}
			}
		case *ast.FuncDecl:
			if d.Name.Name != "ReferenceFor" || d.Recv != nil {
				continue
			}
			if got := len(d.Type.Params.List); got != 1 || types(d.Type.Params.List[0].Type) != "string" {
				t.Fatalf("ReferenceFor parameters are not (name string)")
			}
			if d.Type.Results == nil || len(d.Type.Results.List) != 2 ||
				types(d.Type.Results.List[0].Type) != "Reference" ||
				types(d.Type.Results.List[1].Type) != "bool" {
				t.Fatalf("ReferenceFor results are not (Reference, bool)")
			}
			lookup = true
		}
	}
	if !registry {
		t.Fatal("ReferenceRegistry is not declared")
	}
	if !lookup {
		t.Fatal("ReferenceFor is not declared")
	}
	want := map[string]string{
		"Name": "string", "Family": "GalleryFamily", "Module": "string",
		"Signature": "string", "Summary": "string", "Guidance": "string",
		"Keyboard": "string", "States": "[]string",
	}
	for name, kind := range want {
		if got, ok := fields[name]; !ok || got != kind {
			t.Fatalf("Reference field %s = %q (present=%v), want %q", name, got, ok, kind)
		}
	}
	if len(fields) != len(want) {
		t.Fatalf("Reference has %d fields, want %d: %v", len(fields), len(want), fields)
	}
}

// types renders a type expression as source text for comparison.
func types(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.ArrayType:
		return "[]" + types(e.Elt)
	default:
		return ""
	}
}

// A component that declares no signature, summary, guidance, keyboard contract
// or states has said something specific: it adds no key handling and has one
// rendering state. Emitting empty strings for those would turn that statement
// into visible blank documentation on the reference page.
func TestUIReferenceRegistryOmitsUndeclaredFields(t *testing.T) {
	lock, mods := referenceFixture()
	f, err := emitUIReferenceRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	entry := entryFor(t, f.Content, "badge")
	for _, forbidden := range []string{"Signature:", "Summary:", "Guidance:", "Keyboard:", "States:"} {
		if strings.Contains(entry, forbidden) {
			t.Fatalf("badge entry declares nothing optional but emitted %s: %s", forbidden, entry)
		}
	}
	// The partially declared component keeps exactly what it declared.
	grid := entryFor(t, f.Content, "data-grid")
	if !strings.Contains(grid, "Signature:") || !strings.Contains(grid, "Keyboard:") {
		t.Fatalf("data-grid entry lost a declared field: %s", grid)
	}
	for _, forbidden := range []string{"Summary:", "Guidance:", "States:"} {
		if strings.Contains(grid, forbidden) {
			t.Fatalf("data-grid entry emitted undeclared %s: %s", forbidden, grid)
		}
	}
}

// entryFor returns the single registry literal line for a component name.
func entryFor(t *testing.T, content, name string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, `{Name: "`+name+`"`) {
			return line
		}
	}
	t.Fatalf("no registry entry for %q in:\n%s", name, content)
	return ""
}

// The reference and the component metadata are read together: a detail page
// resolves a name from one and its documentation from the other. Emitting them
// in different orders would make the gallery's listing order depend on which
// registry it happened to iterate.
func TestUIReferenceRegistryMatchesComponentRegistryOrder(t *testing.T) {
	lock, mods := referenceFixture()
	reference, err := emitUIReferenceRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit reference: %v", err)
	}
	components, err := emitUIComponentRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit components: %v", err)
	}
	names := regexp.MustCompile(`\{Name: "([a-z0-9-]+)"`)
	got := names.FindAllStringSubmatch(reference.Content, -1)
	want := names.FindAllStringSubmatch(components.Content, -1)
	if len(got) != len(want) || len(got) == 0 {
		t.Fatalf("registry sizes differ: %d reference entries vs %d component entries", len(got), len(want))
	}
	for i := range got {
		if got[i][1] != want[i][1] {
			t.Fatalf("entry %d: reference %q, component %q", i, got[i][1], want[i][1])
		}
	}
}

// The emitter has to be part of the pipeline, not merely reachable: an emitter
// nobody calls means the file exists once and then rots as modules change.
func TestUIReferenceRegistryIsWiredIntoGenerateAll(t *testing.T) {
	lock, mods := referenceFixture()
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	for _, f := range files {
		if f.Path == "internal/web/templates/ui/reference_gen.go" {
			if !strings.Contains(f.Content, "func ReferenceFor(") {
				t.Fatalf("wired reference registry has no lookup:\n%s", f.Content)
			}
			return
		}
	}
	t.Fatalf("GenerateAll did not emit the ui reference registry: %v", files)
}

// A misspelled state names a rendering state nothing produces: the reference
// would advertise it, the visual matrix would try to capture it, and neither
// would find anything to render.
func TestUIStatesRejectUnknownValue(t *testing.T) {
	items := []UIContribution{{Name: "badge", Family: GalleryFeedback, States: []string{"disbaled"}}}
	err := validateUI(items, true)
	if err == nil {
		t.Fatal("validateUI accepted an unknown rendering state")
	}
	if !strings.Contains(err.Error(), "disbaled") {
		t.Fatalf("error must name the offending state: %v", err)
	}
	// The same list spelled correctly is accepted, so the rejection is about the
	// value and not about declaring states at all.
	items[0].States = []string{"disabled"}
	if err := validateUI(items, true); err != nil {
		t.Fatalf("validateUI rejected a known state: %v", err)
	}
}

// Canonical manifests are the bytes the registry publishes and digests. Two
// orderings of the same states would be two different published files for one
// identical declaration.
func TestUIStatesMustBeSortedWhenCanonical(t *testing.T) {
	items := []UIContribution{{Name: "badge", Family: GalleryFeedback, States: []string{"error", "default"}}}
	if err := validateUI(items, true); err == nil {
		t.Fatal("validateUI accepted unsorted canonical states")
	}
	if err := validateUI(items, false); err != nil {
		t.Fatalf("non-canonical states must not require sorting: %v", err)
	}
	duplicated := []UIContribution{{Name: "badge", Family: GalleryFeedback, States: []string{"default", "default"}}}
	if err := validateUI(duplicated, false); err == nil {
		t.Fatal("validateUI accepted duplicate states")
	}
}

// A signature is published verbatim as the component's public interface. Text
// that is not the renderer's own declaration would document a call that does
// not compile.
func TestUISignatureMustBeATemplDeclaration(t *testing.T) {
	items := []UIContribution{{Name: "badge", Family: GalleryFeedback, Signature: "func Badge(o BadgeOpts)"}}
	if err := validateUI(items, true); err == nil {
		t.Fatal("validateUI accepted a signature that is not a templ declaration")
	}
	items[0].Signature = "templ Badge(o BadgeOpts)"
	if err := validateUI(items, true); err != nil {
		t.Fatalf("validateUI rejected a valid signature: %v", err)
	}
	// Absent is legal: not every component has published its signature yet, and
	// an empty string must not be read as a malformed one.
	items[0].Signature = ""
	if err := validateUI(items, true); err != nil {
		t.Fatalf("validateUI rejected an absent signature: %v", err)
	}
}
