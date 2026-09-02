package gggcli

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The resource generator is exercised through buildCreateFiles rather than
// through its internals: that is the function the create handler calls, so a
// test that passes here is a statement about the command and not only about a
// helper. The registry path is the example registry root, which makes the file
// keys the exact paths the fixture lives at.
const resourceFixtureRegistry = "registry/testdata"

func buildResource(t *testing.T, mutation CreateMutation) (map[string][]byte, modkit.Manifest) {
	t.Helper()
	mutation.Kind = "resource"
	registry := modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: resourceFixtureRegistry}
	files, manifest, err := (&Controller{}).buildCreateFiles(context.Background(), registry, "", mutation)
	if err != nil {
		t.Fatalf("buildCreateFiles(%+v): %v", mutation, err)
	}
	if manifest == nil {
		t.Fatalf("buildCreateFiles(%+v) returned no manifest", mutation)
	}
	return files, *manifest
}

func buildResourceError(t *testing.T, mutation CreateMutation) error {
	t.Helper()
	mutation.Kind = "resource"
	registry := modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: resourceFixtureRegistry}
	_, _, err := (&Controller{}).buildCreateFiles(context.Background(), registry, "", mutation)
	if err == nil {
		t.Fatalf("buildCreateFiles(%+v) was accepted, want a refusal", mutation)
	}
	return err
}

// targets is the set of installed destinations the slice claims, which is what
// an operator sees in the plan.
func targets(manifest modkit.Manifest) []string {
	out := make([]string, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		out = append(out, file.Target)
	}
	sort.Strings(out)
	return out
}

func routeIDs(manifest modkit.Manifest) []string {
	out := make([]string, 0, len(manifest.Runtime.Routes))
	for _, route := range manifest.Runtime.Routes {
		out = append(out, route.ID)
	}
	return out
}

func requirementIDs(manifest modkit.Manifest) []string {
	out := make([]string, 0, len(manifest.Requires))
	for _, requirement := range manifest.Requires {
		out = append(out, requirement.ID)
	}
	return out
}

func payload(t *testing.T, files map[string][]byte, base string) string {
	t.Helper()
	name := resourceFixtureRegistry + "/registry/modules/workflow/widget/payload/" + base
	body, ok := files[name]
	if !ok {
		keys := make([]string, 0, len(files))
		for key := range files {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		t.Fatalf("no payload %s; have %v", base, keys)
	}
	return string(body)
}

// The documented defaults: the table is the snake plural of the name and the
// route is /app/<kebab plural>. Both feed the SQL, the handlers, the templates
// and the declarations, so getting them from one place is what keeps those
// four in step.
func TestCreateResourceDefaultsTableAndRoute(t *testing.T) {
	_, manifest := buildResource(t, CreateMutation{Name: "PurchaseOrder", Scope: "org"})

	if got, want := manifest.Data[0].Table, "purchase_orders"; got != want {
		t.Fatalf("default table = %q, want %q", got, want)
	}
	index := manifest.Runtime.Routes[slices.Index(routeIDs(manifest), "purchase-order.index")]
	if got, want := index.Pattern, "/app/purchase-orders"; got != want {
		t.Fatalf("default route = %q, want %q", got, want)
	}
	if !slices.Contains(targets(manifest), "internal/db/queries/purchase_orders.sql") {
		t.Fatalf("query file is not named after the table: %v", targets(manifest))
	}
}

func TestCreateResourceHonoursTableAndRouteOverrides(t *testing.T) {
	_, manifest := buildResource(t, CreateMutation{
		Name: "Widget", Scope: "org", Table: "inventory_items", Route: "/app/inventory",
	})

	if got, want := manifest.Data[0].Table, "inventory_items"; got != want {
		t.Fatalf("--table override = %q, want %q", got, want)
	}
	if !slices.Contains(targets(manifest), "internal/db/queries/inventory_items.sql") {
		t.Fatalf("--table override did not move the query file: %v", targets(manifest))
	}
	for _, route := range manifest.Runtime.Routes {
		if !strings.HasPrefix(route.Pattern, "/app/inventory") {
			t.Fatalf("--route override left %s at %q", route.ID, route.Pattern)
		}
	}
	// Every query still names the overridden table, or the ownership ledger
	// would guard a table the SQL never touches.
	for _, query := range manifest.Runtime.Queries {
		if query.Table != "inventory_items" {
			t.Fatalf("query %s declares table %q, want the override", query.Name, query.Table)
		}
	}
}

// The plain shape is the whole point of the change: one invocation with no
// optional flags must still produce a slice that serves a request.
func TestCreateResourcePlainShapeIsAServableSlice(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})

	wantTargets := []string{
		"internal/db/queries/widgets.sql",
		"internal/web/templates/widget.templ",
		"internal/web/workflow_widget.go",
		"internal/web/workflow_widget_test.go",
	}
	if got := targets(manifest); !slices.Equal(got, wantTargets) {
		t.Fatalf("targets = %v, want %v", got, wantTargets)
	}
	wantRoutes := []string{"widget.create", "widget.delete", "widget.index", "widget.update"}
	if got := routeIDs(manifest); !slices.Equal(got, wantRoutes) {
		t.Fatalf("routes = %v, want %v", got, wantRoutes)
	}
	if got := manifest.Claims.Routes; !slices.Equal(got, wantRoutes) {
		t.Fatalf("claims.routes = %v, want the declared route ids %v", got, wantRoutes)
	}
	wantQueries := []string{
		"CountWidgets", "CreateWidget", "DeleteWidget", "GetWidgetByID", "ListWidgets", "UpdateWidget",
	}
	got := make([]string, 0, len(manifest.Runtime.Queries))
	for _, query := range manifest.Runtime.Queries {
		got = append(got, query.Name)
	}
	if !slices.Equal(got, wantQueries) {
		t.Fatalf("queries = %v, want %v", got, wantQueries)
	}
	if !slices.Equal(got, manifest.Claims.Queries) {
		t.Fatalf("claims.queries = %v, want the declared query names %v", manifest.Claims.Queries, got)
	}
	// Every declared sqlc method must exist in the emitted query file, or the
	// handlers call a method sqlc never generates.
	queries := string(files[resourceFixtureRegistry+"/registry/modules/workflow/widget/payload/widgets.sql.txt"])
	for _, name := range wantQueries {
		if !strings.Contains(queries, "-- name: "+name+" ") {
			t.Fatalf("query file has no %q annotation:\n%s", name, queries)
		}
	}
	// House rule: every UPDATE sets updated_at. Counting the statements rather
	// than the phrase keeps the check honest when a second UPDATE appears.
	for _, statement := range strings.Split(queries, "\n")[1:] {
		if strings.HasPrefix(statement, "UPDATE ") && !strings.Contains(statement, "updated_at = now()") {
			t.Fatalf("UPDATE does not set updated_at: %q", statement)
		}
	}
	if strings.Count(queries, "\nUPDATE ") != 1 {
		t.Fatalf("expected exactly one UPDATE statement:\n%s", queries)
	}

	if len(manifest.Runtime.Navigation) != 1 || manifest.Runtime.Navigation[0].ID != "nav.app.widget" {
		t.Fatalf("navigation = %+v, want one app entry", manifest.Runtime.Navigation)
	}
	if len(manifest.Runtime.Visual) != 1 || manifest.Runtime.Visual[0].Path != "/app/widgets" {
		t.Fatalf("visual = %+v, want the read page", manifest.Runtime.Visual)
	}
	if got, want := len(manifest.Migrations), 1; got != want {
		t.Fatalf("migrations = %d, want %d", got, want)
	}
	if got := manifest.Data; len(got) != 1 || got[0].Scope != "org" || got[0].OrganizationDelete != "cascade" {
		t.Fatalf("data = %+v, want one org-scoped cascading table", got)
	}
	if !slices.Equal(manifest.Claims.Data, []string{"widgets"}) {
		t.Fatalf("claims.data = %v, want the table", manifest.Claims.Data)
	}
	wantRequires := []string{
		"ggg/system/database", "ggg/system/i18n", "ggg/system/identity",
		"ggg/system/organizations", "ggg/system/security", "ggg/system/server",
	}
	if got := requirementIDs(manifest); !slices.Equal(got, wantRequires) {
		t.Fatalf("requires = %v, want %v", got, wantRequires)
	}
	if manifest.OpenAPI != nil {
		t.Fatal("a resource without --api declared an OpenAPI slice")
	}
	if !slices.Equal(manifest.Tests.GoPackages, []string{"internal/web"}) {
		t.Fatalf("tests.go_packages = %v, want internal/web", manifest.Tests.GoPackages)
	}
}

// Locales must cover every key the template reads, in both catalogs, with
// matching placeholders — generation refuses anything less, and a missing key
// would render as its own name in the UI.
func TestCreateResourceLocalesCoverEveryTemplateKey(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})
	body := string(files[resourceFixtureRegistry+"/registry/modules/workflow/widget/payload/widget.templ.txt"])

	used := map[string]struct{}{}
	for _, fragment := range strings.Split(body, `i18n.T(ctx, "`)[1:] {
		used[fragment[:strings.Index(fragment, `"`)]] = struct{}{}
	}
	if len(used) == 0 {
		t.Fatal("the template reads no i18n keys")
	}
	for key := range used {
		for _, locale := range []string{"en", "es"} {
			if _, ok := manifest.Locales[locale][key]; !ok {
				t.Fatalf("template key %q has no %s translation", key, locale)
			}
		}
	}
	for key := range manifest.Locales["en"] {
		if _, ok := used[key]; !ok && !strings.HasSuffix(key, ".nav") {
			t.Fatalf("locale key %q is declared but nothing reads it", key)
		}
	}
	for key, value := range manifest.Locales["en"] {
		if strings.Count(value, "%") != strings.Count(manifest.Locales["es"][key], "%") {
			t.Fatalf("key %q has mismatched placeholders: en %q, es %q",
				key, value, manifest.Locales["es"][key])
		}
	}
}

func TestCreateResourceAPIAddsTheJSONTransport(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", API: true})

	for _, id := range []string{"api.widget.list", "api.widget.create", "api.widget.update", "api.widget.delete"} {
		index := slices.Index(routeIDs(manifest), id)
		if index < 0 {
			t.Fatalf("--api did not declare %s: %v", id, routeIDs(manifest))
		}
		route := manifest.Runtime.Routes[index]
		if !strings.HasPrefix(string(route.Scope), "api-") {
			t.Fatalf("%s scope = %q, want an api scope", id, route.Scope)
		}
		if !route.Policy.CSRFExempt || route.Policy.CSRFReason == "" {
			t.Fatalf("%s must be CSRF-exempt with a stated reason: %+v", id, route.Policy)
		}
		if !strings.HasPrefix(route.Pattern, "/api/v1/widgets") {
			t.Fatalf("%s pattern = %q, want /api/v1/widgets", id, route.Pattern)
		}
	}
	if manifest.OpenAPI == nil || len(manifest.OpenAPI.Operations) != 4 {
		t.Fatalf("--api must document every route it declares: %+v", manifest.OpenAPI)
	}
	documented := map[string]struct{}{}
	for _, operation := range manifest.OpenAPI.Operations {
		documented[operation.RouteID] = struct{}{}
		if operation.Summary == "" || len(operation.Responses) == 0 {
			t.Fatalf("operation %s is missing a summary or responses", operation.OperationID)
		}
	}
	for _, route := range manifest.Runtime.Routes {
		if !strings.HasPrefix(string(route.Scope), "api-") {
			continue
		}
		if _, ok := documented[route.ID]; !ok {
			t.Fatalf("api route %s has no OpenAPI operation", route.ID)
		}
	}
	for _, id := range []string{"ggg/system/api", "ggg/workflow/openapi-contract"} {
		if !slices.Contains(requirementIDs(manifest), id) {
			t.Fatalf("--api must require %s: %v", id, requirementIDs(manifest))
		}
	}
	transport := payload(t, files, "workflow_widget.go.txt")
	for _, want := range []string{"api.WriteJSON", "api.WriteError", "handleAPIListWidgets"} {
		if !strings.Contains(transport, want) {
			t.Fatalf("transport does not use %s", want)
		}
	}
}

func TestCreateResourceAdminAddsTheStaffReadSurface(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", Admin: true})

	index := slices.Index(routeIDs(manifest), "widget.admin")
	if index < 0 {
		t.Fatalf("--admin declared no admin route: %v", routeIDs(manifest))
	}
	route := manifest.Runtime.Routes[index]
	if route.Scope != modkit.RouteAdmin || route.Method != "GET" || route.Pattern != "/admin/widgets" {
		t.Fatalf("admin route = %+v, want a GET on /admin/widgets at admin scope", route)
	}
	if !slices.ContainsFunc(manifest.Runtime.Navigation, func(n modkit.NavigationContribution) bool {
		return n.Area == modkit.NavAreaAdmin
	}) {
		t.Fatalf("--admin declared no admin navigation: %+v", manifest.Runtime.Navigation)
	}
	// A tenant-scoped table needs untenanted queries for the staff surface, or
	// "admin" would only ever show the operator's own organization.
	names := manifest.Claims.Queries
	for _, want := range []string{"ListAllWidgets", "CountAllWidgets"} {
		if !slices.Contains(names, want) {
			t.Fatalf("--admin did not declare %s: %v", want, names)
		}
	}
	if !strings.Contains(payload(t, files, "widget.templ.txt"), "templ AdminWidgetsPage(") {
		t.Fatal("--admin emitted no admin page renderer")
	}
	if !strings.Contains(payload(t, files, "widget.templ.txt"), "ReadOnly   bool") {
		t.Fatal("the read surface cannot distinguish the staff view")
	}

	_, plain := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})
	if slices.Contains(plain.Claims.Queries, "ListAllWidgets") {
		t.Fatal("the untenanted admin queries leaked into the plain shape")
	}
}

func TestCreateResourceSearchWiresTheIndex(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", Search: true})

	if !slices.Contains(requirementIDs(manifest), "ggg/system/search") {
		t.Fatalf("--search must require the search seam: %v", requirementIDs(manifest))
	}
	transport := payload(t, files, "workflow_widget.go.txt")
	for _, want := range []string{
		"func (s *Server) indexWidget(",
		"func (s *Server) deleteWidgetIndex(",
		`search.Document{TenantID: tenantID, Collection: "widgets"`,
		"s.indexWidget(ctx, org.OrgID,",
		"s.deleteWidgetIndex(ctx, org.OrgID,",
	} {
		if !strings.Contains(transport, want) {
			t.Fatalf("--search transport is missing %q", want)
		}
	}

	_, plain := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})
	if slices.Contains(requirementIDs(plain), "ggg/system/search") {
		t.Fatal("the plain shape requires the search seam it never calls")
	}
}

// --no-ui keeps the data and the optional API and drops the browser surface
// whole: no template, no app routes, no navigation, no visual baseline, no
// locale keys. A leftover declaration there would register a route with no
// renderer.
func TestCreateResourceNoUIDropsTheBrowserSurface(t *testing.T) {
	_, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", NoUI: true})

	wantTargets := []string{"internal/db/queries/widgets.sql"}
	if got := targets(manifest); !slices.Equal(got, wantTargets) {
		t.Fatalf("--no-ui targets = %v, want %v", got, wantTargets)
	}
	if len(manifest.Runtime.Routes) != 0 {
		t.Fatalf("--no-ui declared routes: %v", routeIDs(manifest))
	}
	if len(manifest.Runtime.Navigation) != 0 || len(manifest.Runtime.Visual) != 0 || len(manifest.Locales) != 0 {
		t.Fatalf("--no-ui left a browser declaration behind: %+v", manifest.Runtime)
	}
	if len(manifest.Migrations) != 1 || len(manifest.Data) != 1 || len(manifest.Runtime.Queries) == 0 {
		t.Fatal("--no-ui dropped the data it is supposed to keep")
	}

	_, withAPI := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", NoUI: true, API: true})
	if got := routeIDs(withAPI); !slices.Equal(got,
		[]string{"api.widget.create", "api.widget.delete", "api.widget.list", "api.widget.update"}) {
		t.Fatalf("--no-ui --api routes = %v, want only the JSON transport", got)
	}
	if !slices.Contains(targets(withAPI), "internal/web/workflow_widget.go") {
		t.Fatalf("--no-ui --api emitted no transport: %v", targets(withAPI))
	}
	if slices.Contains(targets(withAPI), "internal/web/templates/widget.templ") {
		t.Fatalf("--no-ui --api emitted a template: %v", targets(withAPI))
	}
}

func TestCreateResourceRefusesNoUIWithAdmin(t *testing.T) {
	err := buildResourceError(t, CreateMutation{Name: "Widget", Scope: "org", NoUI: true, Admin: true})
	if !strings.Contains(err.Error(), "--no-ui cannot be combined with --admin") {
		t.Fatalf("error = %v, want the --no-ui/--admin refusal", err)
	}
	if got := exitOf(t, err); got != exitUsage {
		t.Fatalf("exit = %d, want the usage exit %d", got, exitUsage)
	}
}

// An API token carries an organization and no user, so a user-scoped table has
// no tenant on the JSON transport. Refusing is the only answer that is not a
// cross-user data leak.
func TestCreateResourceRefusesAPIForUserScope(t *testing.T) {
	err := buildResourceError(t, CreateMutation{Name: "Widget", Scope: "user", API: true})
	if !strings.Contains(err.Error(), "--api requires --scope org or platform") {
		t.Fatalf("error = %v, want the user-scope API refusal", err)
	}
}

func TestCreateResourceRefusesUnknownScope(t *testing.T) {
	err := buildResourceError(t, CreateMutation{Name: "Widget", Scope: "tenant"})
	if !strings.Contains(err.Error(), "--scope must be user, org, or platform") {
		t.Fatalf("error = %v, want the scope refusal", err)
	}
}

// Every reachable flag combination must emit Go that parses and is already
// gofmt-canonical. The example closure compiles exactly one of these shapes for
// real; this is what keeps the other eleven honest.
func TestCreateResourceEmitsCanonicalGo(t *testing.T) {
	for _, scope := range []string{"user", "org", "platform"} {
		for _, flags := range []CreateMutation{
			{},
			{API: true},
			{Admin: true},
			{Search: true},
			{API: true, Admin: true, Search: true},
			{NoUI: true},
			{NoUI: true, API: true},
			{NoUI: true, Search: true},
		} {
			mutation := flags
			mutation.Name, mutation.Scope = "Widget", scope
			if mutation.API && scope == "user" {
				continue
			}
			files, _ := buildResource(t, mutation)
			emitted := 0
			for name, body := range files {
				if !strings.HasSuffix(name, ".go.txt") {
					continue
				}
				emitted++
				if _, err := parser.ParseFile(token.NewFileSet(), name, body, parser.SkipObjectResolution); err != nil {
					t.Fatalf("%s %+v does not parse: %v\n%s", scope, flags, err, body)
				}
				if formatted := formatGo(string(body)); formatted != string(body) {
					t.Fatalf("%s %+v is not gofmt-canonical: %s", scope, flags, name)
				}
			}
			if mutation.NoUI && !mutation.API && !mutation.Search {
				if emitted != 0 {
					t.Fatalf("%s %+v emitted Go with no transport to serve", scope, flags)
				}
				continue
			}
			if emitted == 0 {
				t.Fatalf("%s %+v emitted no Go", scope, flags)
			}
		}
	}
}

// The example closure under registry/testdata is the compile proof: `ggg
// registry validate` installs it, runs sqlc and templ over it, builds the
// derivative and runs its test. That proof is only about this generator if the
// fixture is byte-for-byte what the generator emits, which is what this
// asserts. Set GGG_UPDATE_RESOURCE_FIXTURE=1 to rewrite it after a deliberate
// change, then re-run `go run ./cmd/ggg registry validate`.
func TestCreateResourceMatchesExampleFixture(t *testing.T) {
	root := repositoryRoot(t)
	files, _ := buildResource(t, CreateMutation{
		Name: "example-resource", Scope: "org", API: true, Admin: true, Search: true,
	})
	const dir = resourceFixtureRegistry + "/registry/modules/workflow/example-resource"

	if os.Getenv("GGG_UPDATE_RESOURCE_FIXTURE") != "" {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			t.Fatal(err)
		}
		for name, body := range files {
			full := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, body, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("rewrote %d fixture file(s) under %s", len(files), dir)
		return
	}

	for name, want := range files {
		if !strings.HasPrefix(name, dir+"/") {
			t.Fatalf("the generator wrote %s, which is outside the fixture directory", name)
		}
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s differs from what the generator emits; "+
				"re-run with GGG_UPDATE_RESOURCE_FIXTURE=1 if the change is deliberate", name)
		}
	}
	entries := walkFixture(t, filepath.Join(root, filepath.FromSlash(dir)), dir)
	expected := make([]string, 0, len(files))
	for name := range files {
		expected = append(expected, name)
	}
	sort.Strings(expected)
	if !slices.Equal(entries, expected) {
		t.Fatalf("fixture files = %v, want exactly %v", entries, expected)
	}

	// The fixture is only installable if its own manifest parses as one, which
	// is the same check the catalog loader runs.
	var document struct {
		Schema int             `json:"schema"`
		Module modkit.Manifest `json:"module"`
	}
	raw := files[dir+"/module.json"]
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("fixture manifest does not parse: %v", err)
	}
	if got, want := document.Module.ID, "ggg/workflow/example-resource"; got != want {
		t.Fatalf("fixture module id = %q, want %q", got, want)
	}
	if err := modkit.ValidateManifest(document.Module); err != nil {
		t.Fatalf("fixture manifest is invalid: %v", err)
	}
}

func walkFixture(t *testing.T, dir, prefix string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, prefix+"/"+filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

// repositoryRoot walks up from the package directory to the module root, which
// is where every registry path in this package is anchored.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test working directory")
		}
		dir = parent
	}
}

// The command path, not only the builder: `ggg create resource` must preview
// and apply into the mutable project registry, put every payload on disk, and
// leave a manifest the catalog loader accepts.
func TestCreateResourceWritesTheProjectRegistry(t *testing.T) {
	root, engine := cliProject(t)
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema: 2,
		Registries: []modkit.ProjectRegistry{
			{Namespace: "acme", Source: "directory", Path: "registry"},
		},
		Modules: []string{}, Exclude: []string{}, Providers: map[string]modkit.ProviderSelections{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	writeTestFile(t, root, "registry/registry.json", []byte(`{
  "schema": 2,
  "namespace": "acme",
  "canonical_module": "example.com/acme/app",
  "includes": [
    "registry/elements.json",
    "registry/components.json",
    "registry/pages.json",
    "registry/workflows.json",
    "registry/systems.json",
    "registry/profiles.json"
  ]
}
`))

	if _, _, runErr := runApp(t, root, engine,
		"create", "resource", "Widget", "--scope", "org", "--api", "--search"); runErr != nil {
		t.Fatalf("create resource: %v", runErr)
	}

	base := filepath.Join(root, "registry", "registry", "modules", "workflow", "widget")
	for _, name := range []string{
		"module.json",
		filepath.Join("payload", "widgets.sql.txt"),
		filepath.Join("payload", "widget.sql"),
		filepath.Join("payload", "widget.templ.txt"),
		filepath.Join("payload", "workflow_widget.go.txt"),
		filepath.Join("payload", "workflow_widget_test.go.txt"),
	} {
		if _, statErr := os.Stat(filepath.Join(base, name)); statErr != nil {
			t.Fatalf("create resource did not write %s: %v", name, statErr)
		}
	}

	// The index the command rebuilt must publish the new module and the catalog
	// loader must accept it: a module the catalog cannot read is a module
	// `ggg add` can never install.
	catalog, err := modkit.LoadCatalog(os.DirFS(filepath.Join(root, "registry")))
	if err != nil {
		t.Fatalf("load the project registry: %v", err)
	}
	if !slices.ContainsFunc(catalog.Modules, func(m modkit.Manifest) bool { return m.ID == "acme/workflow/widget" }) {
		t.Fatalf("the project catalog does not publish acme/workflow/widget")
	}
}
