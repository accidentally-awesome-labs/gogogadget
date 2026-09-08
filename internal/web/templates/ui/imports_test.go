package ui

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AGENTS.md states the seam the whole ui layer rests on: "`ui` imports templ +
// stdlib only, never `templates`/`billing`/`identity`/sqlc." It then credited
// contract_test.go and control-id_test.go with holding it, and neither reads an
// import: `grep` for Imports/ImportPath/ast.ImportSpec over all of internal/web
// returned nothing, and modkit has no forbidden-import validator either.
//
// One half was enforced by accident. `templates` imports `ui`, so a `ui`
// import of `templates` is an import cycle the compiler refuses. `billing`,
// `identity` and sqlc close no cycle, so nothing refused them: adding both to
// shared.go with `var plantPlan billing.Plan` / `var plantRow sqlc.Project`
// built clean and left every test in both packages green.
//
// A stated claim with a named guard that does not implement it is worse than
// an unguarded claim, because a reader who checks the attribution stops there.
//
// The allow-list is one module prefix. Everything else must be stdlib, and
// stdlib is DERIVED rather than listed: a Go import path is a module path if
// and only if its first element carries a dot, which is the same rule the
// toolchain applies. So a new stdlib package needs no entry here and a new
// vendor cannot arrive as one.
const templModule = "github.com/a-h/templ"

// The real import graph of this package, against the stated allow-list.
func TestUIImportsTemplAndStdlibOnly(t *testing.T) {
	imports, files, specs := uiPackageImports(t)

	// Floors. An AST scan that stopped parsing, a glob that stopped matching
	// and a package that genuinely imports nothing all look identical from the
	// outside, and only the third would be good news. So the file count, the
	// count of files carrying imports, and the presence of the one import this
	// package cannot function without are all asserted.
	require.Greater(t, files, 100,
		"only %d non-test .go files parsed in this package; the scan is looking in the wrong place", files)
	require.Greater(t, specs, 100,
		"only %d import declarations read across %d files; the scan has collapsed, not the package", specs, files)
	require.Greater(t, len(imports), 5,
		"only %d distinct import paths found; a package of 145 renderers reaches for more than that", len(imports))
	require.Contains(t, uiImportedPaths(imports), templModule,
		"no file in this package imports templ, which a package of templ components cannot be true of")

	for path, importers := range imports {
		if strings.HasPrefix(path, templModule) || uiIsStdlib(path) {
			continue
		}
		sort.Strings(importers)
		assert.Failf(t, "ui seam breach",
			"package ui imports %q (from %s).\n"+
				"fix: ui is the universal base every component and every page depends on — it takes templ and "+
				"stdlib and nothing else. A component that needs domain data takes it as a field on its options "+
				"struct, typed with a shape declared in this package, so the caller does the importing.",
			path, strings.Join(importers, ", "))
	}
}

// uiPackageImports maps every import path this package declares to the files
// that declare it, and reports how many files were parsed and how many import
// declarations were read.
//
// The population is the directory, not a list: every non-test .go file,
// generated output included, because a generated file that imported a provider
// SDK would breach the seam exactly as loudly as a hand-written one. Test
// files are out — testify is a test-only dependency and a test import creates
// no edge in the shipped graph — and that is the only exclusion.
//
// Direct imports are enough to close the claim. There is no path from ui to
// billing, identity or sqlc that does not begin with one non-stdlib,
// non-templ import in one of these files, so a transitive breach is a direct
// breach one file earlier.
func uiPackageImports(t *testing.T) (map[string][]string, int, int) {
	t.Helper()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	fset := token.NewFileSet()
	out := map[string][]string{}
	parsed, specs := 0, 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		require.NoErrorf(t, parseErr, "parse %s", name)
		parsed++
		for _, spec := range file.Imports {
			path, unquoteErr := strconv.Unquote(spec.Path.Value)
			require.NoErrorf(t, unquoteErr, "%s declares an unparseable import %s", name, spec.Path.Value)
			out[path] = append(out[path], name)
			specs++
		}
	}
	return out, parsed, specs
}

func uiImportedPaths(imports map[string][]string) []string {
	out := make([]string, 0, len(imports))
	for path := range imports {
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// uiIsStdlib reports whether an import path names a standard-library package.
// The first path element of every module path contains a dot (a hostname);
// stdlib paths never do. Deriving it beats listing it: `slices` and `maps`
// would both have been missing from any list written before they existed.
func uiIsStdlib(path string) bool {
	first, _, _ := strings.Cut(filepath.ToSlash(path), "/")
	return !strings.Contains(first, ".")
}

// A guard that reads the import graph must be seen to reject the breach it
// exists for, or it is the same unverified attribution it replaces. The
// fixtures are the four packages AGENTS.md names by hand, plus one arbitrary
// third party, checked through the same predicate the scan above uses.
func TestUISeamPredicateRejectsTheNamedPackages(t *testing.T) {
	for _, path := range []string{
		"github.com/gogogadget/gogogadget/internal/web/templates",
		"github.com/gogogadget/gogogadget/internal/billing",
		"github.com/gogogadget/gogogadget/internal/identity",
		"github.com/gogogadget/gogogadget/internal/db/sqlc",
		"github.com/stretchr/testify/require",
	} {
		assert.Falsef(t, uiIsStdlib(path) || strings.HasPrefix(path, templModule),
			"the seam predicate accepts %q, so the scan above would accept it too", path)
	}
	for _, path := range []string{"strings", "encoding/json", "slices", templModule, templModule + "/runtime"} {
		assert.Truef(t, uiIsStdlib(path) || strings.HasPrefix(path, templModule),
			"the seam predicate refuses %q, so the scan above would report this package's own imports as breaches", path)
	}
}
