package modkit

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The no-orphan-tests rule, made mechanical.
//
// A module that advertises tests.e2e promises a derivative "install me and you
// can run this spec". That promise is only true if every surface the spec
// navigates is also installed, and the only install-time guarantee a module
// has is its own routes plus the routes of its transitive requires. A spec
// whose owner cannot reach one of the paths it visits is an orphan: a minimal
// or profile install pulls the spec in and the spec 404s on a page nobody
// installed.
//
// So: for every manifest-declared spec, resolve each literal navigation target
// against the same route table the router is generated from, and require the
// declaring module to reach it.
//
// Only literal targets are checked. A computed target (`surface.path`, a loop
// variable, a template literal with a substitution) is deliberately skipped —
// those come from the generated Playwright inventory, which is validated where
// it is emitted, and guessing at their values would make this check lie in
// both directions. Paths that resolve to no catalog route at all are also
// skipped: a 404 assertion or a page.route() fixture URL belongs to no module
// and cannot be broken by an absent one.
func TestEveryDeclaredE2ESpecIsReachableFromItsOwner(t *testing.T) {
	root := specRepoRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	owners, reach := specOwnership(t, catalog.Modules)
	surfaces := surfaceRouteTable(catalog.Modules)

	for _, spec := range sortedSpecKeys(owners) {
		owner := owners[spec]
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(spec)))
		if err != nil {
			t.Fatalf("%s is declared by %s but cannot be read: %v", spec, owner, err)
		}
		for _, target := range literalNavigationTargets(string(body)) {
			pattern, declarers := surfaces.resolve(target)
			if pattern == "" {
				continue
			}
			reachable := false
			for _, module := range declarers {
				if module == owner || reach[owner][module] {
					reachable = true
					break
				}
			}
			if !reachable {
				t.Errorf("orphan e2e test: %s is owned by %s but navigates %s, which route %q "+
					"declares in %v — no module %s can reach. Move the spec to an owner that "+
					"reaches that surface, split it along module lines, or declare the missing requires.",
					spec, owner, target, pattern, declarers, owner)
			}
		}
	}
}

// Ownership is exclusive in both directions: every spec on disk is advertised
// by exactly one manifest, and every advertised spec is a file that manifest
// owns. Without the first half a spec can sit in e2e/ owned by nobody and run
// in a derivative that installed none of its feature; without the second half
// a manifest can advertise a spec it never ships.
func TestEveryE2ESpecOnDiskHasExactlyOneOwner(t *testing.T) {
	root := specRepoRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	declared := map[string][]string{}
	for _, module := range catalog.Modules {
		owned := map[string]bool{}
		for _, file := range module.Files {
			owned[file.Target] = true
		}
		for _, spec := range module.Tests.E2E {
			declared[spec] = append(declared[spec], module.ID)
			if !owned[spec] {
				t.Errorf("%s advertises tests.e2e %q without owning that file", module.ID, spec)
			}
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "e2e"))
	if err != nil {
		t.Fatalf("read e2e directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".spec.ts") {
			continue
		}
		spec := "e2e/" + entry.Name()
		switch len(declared[spec]) {
		case 1:
		case 0:
			t.Errorf("%s is in no manifest's tests.e2e, so no module install brings it", spec)
		default:
			t.Errorf("%s is advertised by %v; exactly one module must own it", spec, declared[spec])
		}
	}
	for spec := range declared {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(spec))); err != nil {
			t.Errorf("tests.e2e names %s, which does not exist: %v", spec, err)
		}
	}
}

// specOwnership maps each advertised spec to its declaring module, and each
// module to the set of modules it can reach through transitive requires.
func specOwnership(t *testing.T, graph []Manifest) (map[string]string, map[string]map[string]bool) {
	t.Helper()
	requires := make(map[string][]string, len(graph))
	owners := map[string]string{}
	for _, module := range graph {
		for _, requirement := range module.Requires {
			requires[module.ID] = append(requires[module.ID], requirement.ID)
		}
		for _, spec := range module.Tests.E2E {
			owners[spec] = module.ID
		}
	}
	reach := make(map[string]map[string]bool, len(owners))
	for spec := range owners {
		owner := owners[spec]
		if reach[owner] != nil {
			continue
		}
		closure := map[string]bool{}
		stack := append([]string(nil), requires[owner]...)
		for len(stack) > 0 {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if closure[next] {
				continue
			}
			closure[next] = true
			stack = append(stack, requires[next]...)
		}
		reach[owner] = closure
	}
	return owners, reach
}

// surfaceTable resolves a request path exactly the way the generated router
// does, because it is an http.ServeMux carrying the same patterns.
type surfaceTable struct {
	mux       *http.ServeMux
	byPattern map[string][]string
}

// surfaceRouteTable indexes every app, admin, and public route in the catalog,
// including the routes a content type expands into. Dev, static, api, webhook,
// and probe scopes are left out: they are not the app/admin/public surface an
// installed page owns, and a spec driving /dev/gallery is asserting the
// component gallery rather than a feature page.
func surfaceRouteTable(graph []Manifest) *surfaceTable {
	table := &surfaceTable{mux: http.NewServeMux(), byPattern: map[string][]string{}}
	for _, route := range selectedRoutes(Lock{}, graph) {
		switch route.contrib.Scope {
		case RouteApp, RouteAdmin, RoutePublic:
		default:
			continue
		}
		pattern := route.contrib.Pattern
		declarers := table.byPattern[pattern]
		if !contains(declarers, route.moduleID) {
			table.byPattern[pattern] = append(declarers, route.moduleID)
		}
	}
	for pattern := range table.byPattern {
		sort.Strings(table.byPattern[pattern])
		table.mux.Handle(pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}
	return table
}

// resolve returns the matched pattern and the modules declaring it, or an empty
// pattern when no catalog route serves the path.
func (t *surfaceTable) resolve(target string) (string, []string) {
	path := target
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "", nil
	}
	_, pattern := t.mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
	if pattern == "" {
		return "", nil
	}
	return pattern, t.byPattern[pattern]
}

// navigationCall matches the navigation forms the specs actually use:
// page.goto, a new page's goto, and the request fixture's verbs — with a
// single-token string argument. A non-literal argument does not match, which
// is the point.
var navigationCall = regexp.MustCompile(
	`(?:\.goto|request\.(?:get|post|put|patch|delete|head|fetch))\(\s*` +
		"(?:'([^'\\n]*)'|\"([^\"\\n]*)\"|`([^`\\n]*)`)")

func literalNavigationTargets(body string) []string {
	seen := map[string]bool{}
	var targets []string
	for _, match := range navigationCall.FindAllStringSubmatch(body, -1) {
		literal := match[1] + match[2] + match[3]
		if literal == "" || strings.Contains(literal, "${") || seen[literal] {
			continue
		}
		seen[literal] = true
		targets = append(targets, literal)
	}
	sort.Strings(targets)
	return targets
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedSpecKeys(owners map[string]string) []string {
	specs := make([]string, 0, len(owners))
	for spec := range owners {
		specs = append(specs, spec)
	}
	sort.Strings(specs)
	return specs
}

func specRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}
