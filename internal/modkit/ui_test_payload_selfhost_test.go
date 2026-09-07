// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. It type-checks THIS repository's
// internal/web/templates/ui package WITH its test files and holds the
// manifests against them.

package modkit

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	gotypes "go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEveryUITestPayloadCompilesInItsOwnClosure is the test-file half of the
// derivation TestTheDeclaredUIEdgesAreTheOnesTheCodeReferences performs over
// product code, and it exists because the product half deliberately cannot see
// this class of coupling.
//
// A test payload is installed by whoever installs its module, and it is
// compiled by `go test ./...` in the created project. So a `_test.go` that
// names a renderer another module owns needs the same declaration a `.templ`
// would: without it, a closure that installs the payload and not the target
// writes a tree where `go build ./...` is green and `go test ./...` does not
// compile. That shipped — `ggg new --profile ggg/profile/minimal` produced a
// tree with 184 type errors across eleven test payloads, all of them this.
//
// The reference is resolved with go/types rather than by name for the same
// reason the product derivation is: the two generated registries name every
// installed module as a string literal, so a grep reads 144-way fan-out out of
// files no module owns.
func TestEveryUITestPayloadCompilesInItsOwnClosure(t *testing.T) {
	repo, err := filepath.Abs("../..")
	require.NoError(t, err)
	catalog, err := LoadCatalog(os.DirFS(repo))
	require.NoError(t, err)

	ownerOfTarget := map[string]string{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			ownerOfTarget[file.Target] = module.ID
		}
	}
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
	dir := filepath.Join(repo, filepath.FromSlash(uiDeriveDir))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var parsed []*ast.File
	tests := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		require.NoError(t, parseErr)
		parsed = append(parsed, file)
		if strings.HasSuffix(name, "_test.go") {
			tests++
		}
	}
	require.Greater(t, tests, 50, "the scan found suspiciously few test payloads")

	info := &gotypes.Info{Uses: map[*ast.Ident]gotypes.Object{}}
	uiPackage, err := (&gotypes.Config{Importer: importer.ForCompiler(fset, "source", nil)}).
		Check(uiDeriveDir, fset, parsed, info)
	require.NoError(t, err, "the ui package does not type-check with its tests, so no derivation means anything")

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

	reach := requirementReach(catalog.Modules)
	undeclared := map[string]bool{}
	for ident, object := range info.Uses {
		file := fset.Position(ident.Pos()).Filename
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		from, referencing := ownerOfTarget[authoredTarget(file)]
		if !referencing {
			continue
		}
		to, owned := ownerOfTarget[definingTarget(object)]
		if !owned || from == to {
			continue
		}
		if _, reachable := reach[from][to]; reachable {
			continue
		}
		undeclared[authoredTarget(file)+" names "+ident.Name+", which "+to+" owns, "+
			"and "+from+" declares no requires path to it"] = true
	}
	listed := make([]string, 0, len(undeclared))
	for line := range undeclared {
		listed = append(listed, line)
	}
	sort.Strings(listed)
	require.Emptyf(t, listed,
		"%d test payload reference(s) no closure guarantees:\n%s",
		len(listed), strings.Join(listed, "\n"))
}
