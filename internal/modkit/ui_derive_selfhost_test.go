// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. It type-checks THIS repository's
// internal/web/templates/ui package and holds the manifests against it.

package modkit

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	gotypes "go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// uiDeriveDir is the one Go package every component and element module
// contributes a file to.
const uiDeriveDir = "internal/web/templates/ui"

// TestTheDeclaredUIEdgesAreTheOnesTheCodeReferences is the derivation, run as
// an assertion. It is how the edges in the manifests were produced and it is
// how to reproduce them:
//
//	go test ./internal/modkit -run TestTheDeclaredUIEdgesAreTheOnesTheCodeReferences -v
//
// # What it derives
//
// internal/web/templates/ui is 145 modules in ONE Go package, so "which module
// does this file need" is not a question about imports. It type-checks the
// package with go/types and resolves every reference through
// TypesInfo.Uses[ident] -> gotypes.Object, attributes the object to the file at
// obj.Pos() (methods and fields to their receiver type's file), and maps that
// file to the module whose manifest declares it — `badge_templ.go` is the templ
// compiler's output for `badge.templ`, which is the payload a module owns.
// Consumers outside the package are resolved the same way: the qualifier comes
// from the file's own import block and the SYMBOL from the type-checked
// package's scope.
//
// # The two derivations that produce a degenerate answer
//
// Both were tried before this one and both said, in effect, that every module
// depends on every other:
//
// Test files. ui-core's contract_test.go holds a hand-written renderers()
// table naming 175 renderers owned by all 144 other modules, so including
// test-file edges collapses the package into a single strongly-connected
// component of 145 and closes every demand set to the whole package. Product
// code only, therefore: no _test.go is type-checked and none is walked.
//
// Names and text. reference_gen.go and components_registry_gen.go each name
// all 144 modules by STRING LITERAL (`{Name: "badge", Module:
// "ggg/component/badge"}`), so a name- or grep-based derivation reads 144-way
// fan-out out of two generated files that no module owns. Resolving objects
// rather than names makes those files contribute their two actual references
// and nothing else, and they are skipped as sources anyway because a generated
// file is rendered FROM the installed set.
//
// # What the numbers are, so a change to them is visible
//
// The composition sub-graph — non-core to non-core — is 134 edges over 144
// nodes, which is 0.65% of the possible edges, and it is acyclic. ui-core is
// referenced by all 144 and references none of them, which is what lets it be
// declared once per module instead of discovered 144 times; the one back-edge
// that used to exist (ui-core's shared.go calling menuItemClass, defined in
// dropdown-menu.templ) was a requires cycle waiting to be declared and the
// function was moved to sit beside the MenuItem type it reads.
func TestTheDeclaredUIEdgesAreTheOnesTheCodeReferences(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	catalog, err := LoadCatalog(os.DirFS(repo))
	require.NoError(t, err)

	ownerOfTarget := map[string]string{}
	payloadCount := map[string]int{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			ownerOfTarget[file.Target] = module.ID
		}
		payloadCount[module.ID] = len(module.Files)
	}
	// authoredTarget maps a compiled Go file back to the payload a manifest
	// owns. Everything else in this test speaks in payload targets.
	authoredTarget := func(path string) string {
		relative, relErr := filepath.Rel(repo, path)
		if relErr != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		if after, ok := strings.CutSuffix(relative, "_templ.go"); ok {
			return after + ".templ"
		}
		return relative
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(filepath.Join(repo, filepath.FromSlash(uiDeriveDir)))
	require.NoError(t, err)
	var parsed []*ast.File
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset,
			filepath.Join(repo, filepath.FromSlash(uiDeriveDir), name), nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr)
		parsed = append(parsed, file)
	}
	require.Greater(t, len(parsed), 100, "the ui package scan found suspiciously few files")

	info := &gotypes.Info{Uses: map[*ast.Ident]gotypes.Object{}}
	uiPackage, err := (&gotypes.Config{Importer: importer.ForCompiler(fset, "source", nil)}).
		Check(uiDeriveDir, fset, parsed, info)
	require.NoError(t, err, "the ui package does not type-check, so no derivation from it means anything")

	// definingTarget is the payload that defines an object. A method or a field
	// belongs to its receiver type's file: `o.Label` inside field.templ is a
	// reference to label.templ's struct, not to wherever the selector is
	// written.
	definingTarget := func(object gotypes.Object) string {
		if object == nil || object.Pkg() != uiPackage {
			return ""
		}
		position := object.Pos()
		if function, ok := object.(*gotypes.Func); ok {
			if signature, ok := function.Type().(*gotypes.Signature); ok && signature.Recv() != nil {
				if named := receiverNamed(signature.Recv().Type()); named != nil {
					position = named.Obj().Pos()
				}
			}
		}
		if !position.IsValid() {
			return ""
		}
		return authoredTarget(fset.Position(position).Filename)
	}

	derived := map[string]map[string]bool{}
	record := func(fromFile string, object gotypes.Object) {
		target := definingTarget(object)
		if target == "" {
			return
		}
		to, owned := ownerOfTarget[target]
		from, referencing := ownerOfTarget[authoredTarget(fromFile)]
		// An unowned definition is one of the generated registries, which no
		// module declares; an unowned reference site is a payload outside the
		// registry (there are none, and a new one should not silently add
		// edges).
		if !owned || !referencing || from == to {
			return
		}
		if derived[from] == nil {
			derived[from] = map[string]bool{}
		}
		derived[from][to] = true
	}

	for ident, object := range info.Uses {
		file := fset.Position(ident.Pos()).Filename
		if isGeneratedRegistrySource(file) {
			continue
		}
		record(file, object)
	}

	require.NoError(t, filepath.WalkDir(repo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			// Named at the repository root, so `internal/web/templates` — where
			// every consumer of the package lives — is not skipped along with
			// the publisher template at `templates/`.
			relative, _ := filepath.Rel(repo, path)
			switch filepath.ToSlash(relative) {
			case ".git", "node_modules", "tmp", "bin", "registry", "templates", "e2e", "static", "content":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if isGeneratedRegistrySource(path) {
			return nil
		}
		if strings.HasPrefix(authoredTarget(path), uiDeriveDir+"/") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return nil
		}
		local := ""
		for _, imported := range file.Imports {
			value := strings.Trim(imported.Path.Value, `"`)
			if !addressesUIPackage(value) {
				continue
			}
			local = "ui"
			if imported.Name != nil {
				local = imported.Name.Name
			}
		}
		if local == "" {
			return nil
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			qualifier, ok := selector.X.(*ast.Ident)
			if !ok || qualifier.Name != local {
				return true
			}
			record(path, uiPackage.Scope().Lookup(selector.Sel.Name))
			return true
		})
		return nil
	}))

	// The graph's shape, asserted so a change to it is a visible one rather
	// than a number in a report nobody re-derives.
	composition := 0
	for from, targets := range derived {
		if !isUIModuleID(from) {
			continue
		}
		for to := range targets {
			if isUIModuleID(to) && to != uiCoreModuleID {
				composition++
			}
		}
	}
	require.Equal(t, 134, composition,
		"the non-core ui composition sub-graph moved; re-read the derivation before changing this number")
	require.Empty(t, derived[uiCoreModuleID],
		"ggg/element/ui-core references %v, so it can no longer be the package's universal base and "+
			"declaring the edge would be a requires cycle", derived[uiCoreModuleID])

	declared := map[string]map[string]bool{}
	for _, module := range catalog.Modules {
		declared[module.ID] = map[string]bool{}
		for _, requirement := range module.Requires {
			if isUIModuleID(requirement.ID) {
				declared[module.ID][requirement.ID] = true
			}
		}
	}

	var undeclared, stale []string
	for from, targets := range derived {
		for to := range targets {
			if !declared[from][to] {
				undeclared = append(undeclared, from+" -> "+to)
			}
		}
	}
	for from, targets := range declared {
		// A module that owns no payload cannot reference anything; two
		// placeholder ui modules own nothing and declare ui-core.
		if payloadCount[from] == 0 {
			continue
		}
		for to := range targets {
			if !derived[from][to] {
				stale = append(stale, from+" -> "+to)
			}
		}
	}
	sort.Strings(undeclared)
	sort.Strings(stale)
	require.Emptyf(t, undeclared,
		"%d module edge(s) the code has and the manifests do not; a profile that stops naming the target "+
			"installs a tree that does not compile", len(undeclared))
	require.Emptyf(t, stale,
		"%d declared edge(s) no product code needs; they over-include every closure that installs the source",
		len(stale))
}

// receiverNamed unwraps a method receiver to the named type it belongs to.
func receiverNamed(typ gotypes.Type) *gotypes.Named {
	for {
		switch actual := typ.(type) {
		case *gotypes.Pointer:
			typ = actual.Elem()
		case *gotypes.Named:
			return actual
		default:
			return nil
		}
	}
}

// isGeneratedRegistrySource reports whether a path is one of the generated
// registries. They name every installed module by string literal, and they are
// rendered from the installed set, so they can neither own an edge nor need one.
func isGeneratedRegistrySource(path string) bool {
	base := filepath.Base(path)
	if strings.HasSuffix(base, "_templ.go") {
		return false
	}
	return strings.HasSuffix(base, "_gen.go")
}

const uiCoreModuleID = "ggg/element/ui-core"

func isUIModuleID(id string) bool {
	return strings.HasPrefix(id, "ggg/component/") || strings.HasPrefix(id, "ggg/element/")
}
