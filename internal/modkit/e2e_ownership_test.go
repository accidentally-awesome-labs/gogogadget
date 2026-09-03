// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

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

// harnessTarget is the file that makes a Playwright suite runnable at all.
// Whoever owns it owns the config, the global setup, the helpers and the
// pinned node dependencies every spec imports.
const harnessTarget = "e2e/playwright.config.ts"

// The no-orphan-tests rule, made mechanical.
//
// A module that advertises tests.e2e promises a derivative "install me and you
// can run this spec". That promise is only true if every surface the spec
// navigates is also installed, and the only install-time guarantee a module
// has is its own routes plus the routes of its transitive requires. A spec
// whose owner cannot reach one of the paths it visits is an orphan: a profile
// install pulls the spec in and the spec 404s on a page nobody installed.
//
// So: for every manifest-declared spec, resolve each literal navigation target
// — method and path — against the same route table the router is generated
// from, and require the declaring module to reach the module that serves it.
//
// What this does NOT see, stated so nobody reads more into a green run than is
// there:
//
//   - click navigation. `getByRole('link', …).click()` and the
//     `toHaveURL`/`waitForURL` destination it lands on are not navigation
//     calls, so a spec can reach a page through the shell without declaring
//     anything. Four `requires` edges in this catalog exist for exactly that
//     and are not defended here.
//   - computed targets: `page.goto(surface.path)` from the generated
//     inventory, a template literal with a substitution, a path built from a
//     variable. Guessing at their values would make the check lie in both
//     directions.
//   - paths served only by an environment-selected adapter, such as
//     billing-local's `/app/billing/confirm`. Which adapter is active is a
//     per-environment project decision, so crediting or refusing it needs an
//     adapter-aware answer this check does not attempt.
//
// A path that resolves to no catalog route is skipped: a 404 assertion or a
// page.route() fixture URL belongs to no module and cannot be broken by an
// absent one.
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
				t.Errorf("orphan e2e test: %s is owned by %s but navigates %s %s, which route %q "+
					"declares in %v — no module %s can reach. Move the spec to an owner that "+
					"reaches that surface, split it along module lines, or declare the missing requires.",
					spec, owner, target.method, target.path, pattern, declarers, owner)
			}
		}
	}
}

// Ownership is exclusive in both directions, and every owner must be able to
// install the harness. Without exclusive ownership a spec can sit in e2e/
// owned by nobody, or be advertised by a module that never ships it. Without
// the harness edge a derivative that installs one feature gets its spec but no
// playwright.config.ts, no helpers and no package.json — the spec cannot even
// resolve its imports, which is the same orphan defect wearing a different
// hat.
func TestEveryE2ESpecOnDiskHasExactlyOneOwner(t *testing.T) {
	root := specRepoRoot(t)
	catalog, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}

	harness := ""
	declared := map[string][]string{}
	for _, module := range catalog.Modules {
		owned := map[string]bool{}
		for _, file := range module.Files {
			owned[file.Target] = true
			if file.Target == harnessTarget {
				harness = module.ID
			}
		}
		for _, spec := range module.Tests.E2E {
			declared[spec] = append(declared[spec], module.ID)
			if !owned[spec] {
				t.Errorf("%s advertises tests.e2e %q without owning that file", module.ID, spec)
			}
		}
	}
	if harness == "" {
		t.Fatalf("no module owns %s", harnessTarget)
	}

	_, reach := specOwnership(t, catalog.Modules)
	for spec, modules := range declared {
		for _, module := range modules {
			if module == harness || reach[module][harness] {
				continue
			}
			t.Errorf("%s is owned by %s, which cannot install the harness %s (owned by %s): the spec "+
				"would land in a tree with no playwright config, helpers or package.json. Add "+
				"%s to its requires.", spec, module, harnessTarget, harness, harness)
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

// specOwnership maps each advertised spec to its declaring module, and every
// module in the catalog to the set it can reach through transitive requires.
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
	reach := make(map[string]map[string]bool, len(graph))
	for _, module := range graph {
		closure := map[string]bool{}
		stack := append([]string(nil), requires[module.ID]...)
		for len(stack) > 0 {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if closure[next] {
				continue
			}
			closure[next] = true
			stack = append(stack, requires[next]...)
		}
		reach[module.ID] = closure
	}
	return owners, reach
}

// surfaceTable resolves a method and path exactly the way the generated router
// does, because it is an http.ServeMux per method carrying the same patterns.
// One mux per method rather than method-qualified patterns in a single mux:
// a probe then never depends on how ServeMux reports a method mismatch, and a
// module that declares only `POST /admin/flags` is not credited with serving a
// GET of that page.
type surfaceTable struct {
	byMethod  map[string]*http.ServeMux
	declarers map[string][]string
}

// surfaceRouteTable indexes every app, admin, and public route in the catalog,
// including the routes a content type expands into. Dev, static, api, webhook,
// and probe scopes are left out: they are not the app/admin/public surface an
// installed page owns, and a spec driving /dev/gallery is asserting the
// component gallery rather than a feature page.
func surfaceRouteTable(graph []Manifest) *surfaceTable {
	table := &surfaceTable{byMethod: map[string]*http.ServeMux{}, declarers: map[string][]string{}}
	for _, route := range selectedRoutes(Lock{}, graph) {
		switch route.contrib.Scope {
		case RouteApp, RouteAdmin, RoutePublic:
		default:
			continue
		}
		key := route.contrib.Method + " " + route.contrib.Pattern
		known := table.declarers[key]
		if contains(known, route.moduleID) {
			continue
		}
		table.declarers[key] = append(known, route.moduleID)
		if len(known) > 0 {
			// Two modules on one method+pattern is refused by
			// validateRoutePatterns; registering twice would panic.
			continue
		}
		mux := table.byMethod[route.contrib.Method]
		if mux == nil {
			mux = http.NewServeMux()
			table.byMethod[route.contrib.Method] = mux
		}
		mux.Handle(route.contrib.Pattern, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}
	for key := range table.declarers {
		sort.Strings(table.declarers[key])
	}
	return table
}

// resolve returns the matched pattern and the modules declaring it, or an empty
// pattern when no catalog route serves that method and path.
func (s *surfaceTable) resolve(target navTarget) (string, []string) {
	mux := s.byMethod[target.method]
	if mux == nil {
		return "", nil
	}
	path := target.path
	if index := strings.IndexAny(path, "?#"); index >= 0 {
		path = path[:index]
	}
	if !strings.HasPrefix(path, "/") {
		return "", nil
	}
	_, pattern := mux.Handler(httptest.NewRequest(target.method, path, nil))
	if pattern == "" {
		return "", nil
	}
	return pattern, s.declarers[target.method+" "+pattern]
}

// navTarget is one navigation a spec performs: the HTTP method the router will
// see, and the literal path.
type navTarget struct {
	method string
	path   string
}

// navigationCall matches the navigation forms the specs use — page.goto, a new
// page's goto, and the request fixture's verbs — with a single string-literal
// argument. A non-literal argument does not match, which is the point.
var navigationCall = regexp.MustCompile(
	`(?:\.goto|request\.(get|post|put|patch|delete|head|fetch))\(\s*` +
		"(?:'([^'\\n]*)'|\"([^\"\\n]*)\"|`([^`\\n]*)`)")

// pathArrayLiteral matches an array whose elements are all string literals —
// the `for (const path of ['/admin', '/admin/users', …])` shape. Those paths
// are navigated through a loop variable, which navigationCall cannot see, and
// the sharpest orphan in this catalog hid in exactly one of them.
var pathArrayLiteral = regexp.MustCompile(
	`\[\s*(` + jsStringAlternation + `(?:\s*,\s*` + jsStringAlternation + `)*)\s*,?\s*\]`)

var jsStringLiteral = regexp.MustCompile(jsStringAlternation)

const jsStringAlternation = "(?:'[^'\\n]*'|\"[^\"\\n]*\"|`[^`\\n]*`)"

func literalNavigationTargets(body string) []navTarget {
	seen := map[navTarget]bool{}
	var targets []navTarget
	add := func(method, literal string) {
		if !strings.HasPrefix(literal, "/") || strings.Contains(literal, "${") {
			return
		}
		target := navTarget{method: method, path: literal}
		if seen[target] {
			return
		}
		seen[target] = true
		targets = append(targets, target)
	}
	for _, match := range navigationCall.FindAllStringSubmatch(body, -1) {
		method := http.MethodGet
		switch strings.ToLower(match[1]) {
		case "", "get", "fetch":
		case "post":
			method = http.MethodPost
		case "put":
			method = http.MethodPut
		case "patch":
			method = http.MethodPatch
		case "delete":
			method = http.MethodDelete
		case "head":
			method = http.MethodHead
		}
		add(method, match[2]+match[3]+match[4])
	}
	// A looped array is always walked with goto, so GET is the only method it
	// can produce.
	for _, array := range pathArrayLiteral.FindAllStringSubmatch(body, -1) {
		for _, element := range jsStringLiteral.FindAllString(array[1], -1) {
			add(http.MethodGet, element[1:len(element)-1])
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].path != targets[j].path {
			return targets[i].path < targets[j].path
		}
		return targets[i].method < targets[j].method
	})
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
