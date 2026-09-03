package gggcli

import (
	"context"
	"go/ast"
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
	files, manifest, _, err := (&Controller{}).buildCreateFiles(context.Background(), registry, "", mutation)
	if err != nil {
		t.Fatalf("buildCreateFiles(%+v): %v", mutation, err)
	}
	if manifest == nil {
		t.Fatalf("buildCreateFiles(%+v) returned no manifest", mutation)
	}
	return files, *manifest
}

func buildResourceDiagnostics(t *testing.T, mutation CreateMutation) []modkit.Diagnostic {
	t.Helper()
	mutation.Kind = "resource"
	registry := modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: resourceFixtureRegistry}
	_, _, diagnostics, err := (&Controller{}).buildCreateFiles(context.Background(), registry, "", mutation)
	if err != nil {
		t.Fatalf("buildCreateFiles(%+v): %v", mutation, err)
	}
	return diagnostics
}

func buildResourceError(t *testing.T, mutation CreateMutation) error {
	t.Helper()
	mutation.Kind = "resource"
	registry := modkit.ProjectRegistry{Namespace: "ggg", Source: "directory", Path: resourceFixtureRegistry}
	_, _, _, err := (&Controller{}).buildCreateFiles(context.Background(), registry, "", mutation)
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

// reachableResourceShape reports whether a flag combination is one the command
// accepts. The two refused shapes are not oversights: an API token carries no
// user, and the search index carries no platform tenant.
func reachableResourceShape(mutation CreateMutation) bool {
	if mutation.API && mutation.Scope == "user" {
		return false
	}
	if mutation.Search && mutation.Scope == "platform" {
		return false
	}
	if mutation.NoUI && mutation.Admin {
		return false
	}
	return true
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

// templateKeys is every i18n key the emitted template reads.
func templateKeys(body string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, fragment := range strings.Split(body, `i18n.T(ctx, "`)[1:] {
		out[fragment[:strings.Index(fragment, `"`)]] = struct{}{}
	}
	return out
}

// Locales must cover every key something actually reads — the template and the
// navigation label keys — in both catalogs, with matching placeholders, and
// must declare nothing else. Generation refuses a missing key, and a key
// nothing reads is dead weight that outlives the string it was written for.
//
// Run across the flag shapes rather than the plain one: --admin is what adds
// the admin_title/admin_nav pair, and a check that only ever sees the default
// shape never looks at them.
func TestCreateResourceLocalesCoverEveryUsedKey(t *testing.T) {
	for _, scope := range []string{"user", "org", "platform"} {
		for _, flags := range []CreateMutation{
			{}, {Admin: true}, {API: true}, {Search: true}, {API: true, Admin: true, Search: true},
		} {
			mutation := flags
			mutation.Name, mutation.Scope = "Widget", scope
			if !reachableResourceShape(mutation) {
				continue
			}
			files, manifest := buildResource(t, mutation)
			used := templateKeys(payload(t, files, "widget.templ.txt"))
			if len(used) == 0 {
				t.Fatalf("%s %+v: the template reads no i18n keys", scope, flags)
			}
			for _, entry := range manifest.Runtime.Navigation {
				used[entry.LabelKey] = struct{}{}
			}
			for key := range used {
				for _, locale := range []string{"en", "es"} {
					if _, ok := manifest.Locales[locale][key]; !ok {
						t.Fatalf("%s %+v: key %q has no %s translation", scope, flags, key, locale)
					}
				}
			}
			for key, value := range manifest.Locales["en"] {
				if _, ok := used[key]; !ok {
					t.Fatalf("%s %+v: locale key %q is declared but nothing reads it", scope, flags, key)
				}
				if strings.Count(value, "%") != strings.Count(manifest.Locales["es"][key], "%") {
					t.Fatalf("%s %+v: key %q has mismatched placeholders: en %q, es %q",
						scope, flags, key, value, manifest.Locales["es"][key])
				}
			}
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
	if !strings.Contains(payload(t, files, "widget.templ.txt"), "Writable   bool") {
		t.Fatal("the read surface cannot distinguish the write surface from the read-only one")
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

// C1 regression. A platform table has no tenant column, so no query predicate
// can scope a write to the caller, and the app middleware group is requireAuth
// → requireNotDisabled → requireOrg → loadPlan with no requireStaff. An
// app-scope POST or DELETE there would hand every authenticated member of every
// organization create, rename and delete on global state. The mutations
// therefore live in the admin group behind the write role, and the JSON
// transport — which has no scope that could impose a staff check — is read-only.
func TestCreateResourcePlatformScopeKeepsWritesOutOfTheAppGroup(t *testing.T) {
	for _, flags := range []CreateMutation{{}, {API: true}, {Admin: true}} {
		mutation := flags
		mutation.Name, mutation.Scope = "Banner", "platform"
		_, manifest := buildResource(t, mutation)

		for _, route := range manifest.Runtime.Routes {
			if route.Scope == modkit.RouteApp && route.Method != "GET" {
				t.Fatalf("%+v: %s %s is an app-scope write on a table with no tenant predicate",
					flags, route.Method, route.Pattern)
			}
			if route.Scope == modkit.RouteAPIWrite {
				t.Fatalf("%+v: %s is a token-authenticated write on a table with no tenant and no staff check",
					flags, route.ID)
			}
		}
		for _, id := range []string{"banner.create", "banner.update", "banner.delete"} {
			index := slices.Index(routeIDs(manifest), id)
			if index < 0 {
				t.Fatalf("%+v: platform scope dropped %s: %v", flags, id, routeIDs(manifest))
			}
			route := manifest.Runtime.Routes[index]
			if route.Scope != modkit.RouteAdmin || !route.Policy.AdminWrite {
				t.Fatalf("%+v: %s = scope %q admin_write %v, want admin scope with the write role",
					flags, id, route.Scope, route.Policy.AdminWrite)
			}
			if !strings.HasPrefix(route.Pattern, "/admin/banners") {
				t.Fatalf("%+v: %s pattern = %q, want /admin/banners", flags, id, route.Pattern)
			}
		}
		// The staff page is where those mutations render, so it is not optional
		// on a platform table however --admin was passed.
		if !slices.Contains(routeIDs(manifest), "banner.admin") {
			t.Fatalf("%+v: platform scope has no staff surface to write from: %v", flags, routeIDs(manifest))
		}
		if !slices.ContainsFunc(manifest.Runtime.Navigation, func(n modkit.NavigationContribution) bool {
			return n.Area == modkit.NavAreaAdmin
		}) {
			t.Fatalf("%+v: platform scope has no admin navigation entry", flags)
		}
	}

	// --api on a platform table documents and serves the read and nothing else.
	_, withAPI := buildResource(t, CreateMutation{Name: "Banner", Scope: "platform", API: true})
	if withAPI.OpenAPI == nil || len(withAPI.OpenAPI.Operations) != 1 ||
		withAPI.OpenAPI.Operations[0].RouteID != "api.banner.list" {
		t.Fatalf("platform --api operations = %+v, want only the list", withAPI.OpenAPI)
	}

	// A tenant-scoped table is unaffected: its owner writes it from the app
	// group, guarded by the tenant predicate in every statement.
	_, org := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})
	create := org.Runtime.Routes[slices.Index(routeIDs(org), "widget.create")]
	if create.Scope != modkit.RouteApp || create.Policy.AdminWrite {
		t.Fatalf("org create = scope %q admin_write %v, want the app group", create.Scope, create.Policy.AdminWrite)
	}
}

// C2 regression. search_documents.tenant_id has a foreign key to orgs, so the
// document tenant is always an organization. A user-scoped row therefore has to
// carry its owner as a filterable field or every private row becomes
// discoverable organization-wide, and a platform row has no owning organization
// at all.
func TestCreateResourceSearchScopesUserRowsByOwner(t *testing.T) {
	files, _ := buildResource(t, CreateMutation{Name: "Note", Scope: "user", Search: true})
	transport := string(files[resourceFixtureRegistry+"/registry/modules/workflow/note/payload/workflow_note.go.txt"])

	for _, want := range []string{
		"func (s *Server) indexNote(ctx context.Context, tenantID, userID, id, name string) {",
		`Fields: map[string]string{"user_id": userID},`,
		"s.indexNote(ctx, org.OrgID, user.UserID, strconv.FormatInt(row.ID, 10), row.Name)",
	} {
		if !strings.Contains(transport, want) {
			t.Fatalf("user-scoped search is missing %q:\n%s", want, transport)
		}
	}

	// The org-scoped document needs no owner field: the tenant already is the
	// owner, so adding one would be noise.
	orgFiles, _ := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", Search: true})
	orgTransport := payload(t, orgFiles, "workflow_widget.go.txt")
	if strings.Contains(orgTransport, `"user_id"`) {
		t.Fatal("org-scoped search filed a user_id field it cannot mean")
	}
	if !strings.Contains(orgTransport, "func (s *Server) indexWidget(ctx context.Context, tenantID, id, name string) {") {
		t.Fatalf("org-scoped search helper has the wrong shape:\n%s", orgTransport)
	}
}

func TestCreateResourceRefusesSearchForPlatformScope(t *testing.T) {
	err := buildResourceError(t, CreateMutation{Name: "Banner", Scope: "platform", Search: true})
	if !strings.Contains(err.Error(), "--search requires --scope user or org") {
		t.Fatalf("error = %v, want the platform search refusal", err)
	}
	if got := exitOf(t, err); got != exitUsage {
		t.Fatalf("exit = %d, want the usage exit %d", got, exitUsage)
	}
}

func TestCreateResourceRefusesUnknownScope(t *testing.T) {
	err := buildResourceError(t, CreateMutation{Name: "Widget", Scope: "tenant"})
	if !strings.Contains(err.Error(), "--scope must be user, org, or platform") {
		t.Fatalf("error = %v, want the scope refusal", err)
	}
}

// resourceShapes is every reachable flag combination, across every scope. The
// example closure compiles exactly one of them for real; these are what keep
// the rest honest.
func resourceShapes() []CreateMutation {
	var out []CreateMutation
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
			if !reachableResourceShape(mutation) {
				continue
			}
			out = append(out, mutation)
		}
	}
	return out
}

// goPayloads is the emitted Go of one shape, parsed, keyed by payload path.
func goPayloads(t *testing.T, files map[string][]byte) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	for name, body := range files {
		if !strings.HasSuffix(name, ".go.txt") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, body, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s does not parse: %v\n%s", name, err, body)
		}
		out[name] = parsed
	}
	return out
}

// Every reachable flag combination must emit Go that parses and is already
// gofmt-canonical.
func TestCreateResourceEmitsCanonicalGo(t *testing.T) {
	for _, mutation := range resourceShapes() {
		files, _ := buildResource(t, mutation)
		emitted := goPayloads(t, files)
		for name, body := range files {
			if _, isGo := emitted[name]; !isGo {
				continue
			}
			if formatted := formatGo(string(body)); formatted != string(body) {
				t.Fatalf("%s %+v is not gofmt-canonical: %s", mutation.Scope, mutation, name)
			}
		}
		if mutation.NoUI && !mutation.API && !mutation.Search {
			if len(emitted) != 0 {
				t.Fatalf("%s %+v emitted Go with no transport to serve", mutation.Scope, mutation)
			}
			continue
		}
		if len(emitted) == 0 {
			t.Fatalf("%s %+v emitted no Go", mutation.Scope, mutation)
		}
	}
}

// Parsing is not enough: an emitted file that calls a function the slice no
// longer emits parses cleanly and fails to compile. That is exactly what
// happened when the unit-test payload and the validator ended up on different
// gates, and `registry validate` only compiles one shape. So every name the
// slice invents and then uses must also be declared by the slice, and every
// handler the manifest declares must exist as a method.
func TestCreateResourceResolvesEverySymbolItEmits(t *testing.T) {
	for _, mutation := range resourceShapes() {
		files, manifest := buildResource(t, mutation)
		emitted := goPayloads(t, files)

		declared := map[string]struct{}{}
		for _, file := range emitted {
			for _, decl := range file.Decls {
				switch node := decl.(type) {
				case *ast.FuncDecl:
					declared[node.Name.Name] = struct{}{}
				case *ast.GenDecl:
					for _, spec := range node.Specs {
						switch named := spec.(type) {
						case *ast.TypeSpec:
							declared[named.Name.Name] = struct{}{}
						case *ast.ValueSpec:
							for _, name := range named.Names {
								declared[name.Name] = struct{}{}
							}
						}
					}
				}
			}
		}

		// The slice's own vocabulary is everything carrying its singular or
		// plural Go identifier. A qualified name (sqlc.…, templates.…) belongs
		// to another package and is excluded; s.<name> is this slice's own
		// method, so it is not.
		owns := func(name string) bool {
			return strings.Contains(name, "Widget") || strings.Contains(name, "widget")
		}
		for path, file := range emitted {
			ast.Inspect(file, func(node ast.Node) bool {
				switch expression := node.(type) {
				case *ast.SelectorExpr:
					receiver, isIdent := expression.X.(*ast.Ident)
					if !isIdent || receiver.Name != "s" {
						// Not a call on this slice's server value, so the name
						// belongs to whatever package or value qualifies it.
						return false
					}
					if owns(expression.Sel.Name) {
						if _, ok := declared[expression.Sel.Name]; !ok {
							t.Fatalf("%s %+v: %s calls s.%s, which the slice never declares",
								mutation.Scope, mutation, path, expression.Sel.Name)
						}
					}
					return false
				case *ast.Ident:
					if owns(expression.Name) {
						if _, ok := declared[expression.Name]; !ok {
							t.Fatalf("%s %+v: %s references %s, which the slice never declares",
								mutation.Scope, mutation, path, expression.Name)
						}
					}
				}
				return true
			})
		}

		for _, route := range manifest.Runtime.Routes {
			if _, ok := declared[route.Handler]; !ok {
				t.Fatalf("%s %+v: route %s declares handler %s, which the slice never emits",
					mutation.Scope, mutation, route.ID, route.Handler)
			}
		}
		// And the reverse: a test payload with nothing to test, or a declared
		// test package the slice contributes no test to, is dead weight.
		_, hasTest := files[resourceFixtureRegistry+
			"/registry/modules/workflow/widget/payload/workflow_widget_test.go.txt"]
		if hasTest != (len(manifest.Tests.GoPackages) != 0) {
			t.Fatalf("%s %+v: test payload present = %v but tests.go_packages = %v",
				mutation.Scope, mutation, hasTest, manifest.Tests.GoPackages)
		}
	}
}

// N2 regression. /app/activity reads the audit table scoped to the viewer's
// organization, so attributing a global change to the acting staff member's
// own organization would publish it into that one tenant's feed. Every shipped
// platform-scoped admin mutation passes an empty org id; these must too.
func TestCreateResourceAuditsPlatformWritesWithoutATenant(t *testing.T) {
	files, _ := buildResource(t, CreateMutation{Name: "Banner", Scope: "platform"})
	transport := string(files[resourceFixtureRegistry+"/registry/modules/workflow/banner/payload/workflow_banner.go.txt"])

	for _, action := range []string{"created", "updated", "deleted"} {
		want := `s.logAudit(ctx, "", user.UserID, "banner.` + action + `"`
		if !strings.Contains(transport, want) {
			t.Fatalf("platform audit is missing %q:\n%s", want, transport)
		}
	}
	if strings.Contains(transport, "org.OrgID") {
		t.Fatalf("a platform mutation still attributes itself to an organization:\n%s", transport)
	}
	if strings.Contains(transport, "org := identity.OrgFrom(ctx)") {
		t.Fatal("a platform mutation still reads an organization it must not use")
	}

	// A tenant-scoped row does belong to an organization, so it keeps it.
	orgFiles, _ := buildResource(t, CreateMutation{Name: "Widget", Scope: "org"})
	orgTransport := payload(t, orgFiles, "workflow_widget.go.txt")
	if !strings.Contains(orgTransport, `s.logAudit(ctx, org.OrgID, user.UserID, "widget.created"`) {
		t.Fatalf("an org-scoped mutation lost its audit organization:\n%s", orgTransport)
	}
}

// N4 regression. The narrowing is right, but three of the four routes --api
// implies are dropped, so the plan has to say so.
func TestCreateResourcePlatformAPINarrowingIsReported(t *testing.T) {
	diagnostics := buildResourceDiagnostics(t, CreateMutation{Name: "Banner", Scope: "platform", API: true})
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v, want exactly one", diagnostics)
	}
	got := diagnostics[0]
	if got.Code != "resource_api_read_only" || got.Severity != "info" {
		t.Fatalf("diagnostic = %+v, want an info resource_api_read_only", got)
	}
	if got.Module != "ggg/workflow/banner" || got.Path != "/api/v1/banners" {
		t.Fatalf("diagnostic names %q at %q, want the module and its API path", got.Module, got.Path)
	}
	for _, want := range []string{"read route only", "/admin/banners"} {
		if !strings.Contains(got.Message, want) {
			t.Fatalf("diagnostic message %q does not mention %q", got.Message, want)
		}
	}

	// Nothing was narrowed for a tenant-scoped table, so nothing is said.
	if got := buildResourceDiagnostics(t, CreateMutation{Name: "Widget", Scope: "org", API: true}); len(got) != 0 {
		t.Fatalf("org --api reported %+v, want no diagnostic", got)
	}
	if got := buildResourceDiagnostics(t, CreateMutation{Name: "Banner", Scope: "platform"}); len(got) != 0 {
		t.Fatalf("platform without --api reported %+v, want no diagnostic", got)
	}
}

// N5 regression. The empty state must not tell the reader to use a form that
// this surface does not render — the app page of a platform table and the
// staff page of a tenant-scoped one both lack it.
func TestCreateResourceEmptyStateMatchesTheSurface(t *testing.T) {
	files, manifest := buildResource(t, CreateMutation{Name: "Widget", Scope: "org", Admin: true})
	body := payload(t, files, "widget.templ.txt")

	if !strings.Contains(body, "func widgetsEmptyBody(ctx context.Context, d WidgetsListData) string {") {
		t.Fatalf("the empty state does not choose its copy by surface:\n%s", body)
	}
	if !strings.Contains(body, `Body:  widgetsEmptyBody(ctx, d),`) {
		t.Fatal("the empty state still hardcodes one body")
	}
	writable, readOnly := manifest.Locales["en"]["widget.empty_body"], manifest.Locales["en"]["widget.empty_body_readonly"]
	if !strings.Contains(writable, "form above") {
		t.Fatalf("writable empty body = %q, want it to point at the form", writable)
	}
	if readOnly == "" || strings.Contains(readOnly, "form") {
		t.Fatalf("read-only empty body = %q, want copy that names no form", readOnly)
	}
	if manifest.Locales["es"]["widget.empty_body_readonly"] == "" {
		t.Fatal("the read-only empty body has no Spanish translation")
	}
}

// exampleResourceFixtures are the shapes registry/testdata publishes as
// installable closures, and they are the generator's only standing compiler
// coverage: `ggg registry validate` installs each one, runs sqlc and templ over
// it, builds the derivative, runs whatever tests it declares, removes it and
// asserts the tree came back byte for byte.
//
// Two shapes rather than one, because they exercise disjoint halves of the
// generator and one of them is where a defect actually shipped:
//
//   - example-resource (--scope org --api --admin --search) is the full slice:
//     browser read page, four app-scope mutations, staff read surface, JSON
//     CRUD, search wiring, locales, navigation, visual baseline.
//   - example-feed (--scope platform --no-ui --api) is the narrowed one: no
//     templates, no locales, no navigation, a read-only JSON transport, no
//     validator and therefore no emitted unit test, and a trimmed dependency
//     and requirement set. That combination is what broke when the unit test
//     and the validator ended up on different gates — it parsed and gofmt'd
//     cleanly and did not compile — so it is on a compiler now rather than
//     only in an AST cross-check.
//   - example-notice (--scope platform, no flags) is the third because the
//     first two leave real branches uncompiled. It is the only shape that
//     reaches the bare single-argument sqlc calls (Get/Create/Delete on a
//     table with no tenant predicate), the empty audit organization —
//     auditOrg()'s "" branch, which also removes a local and so sits in the
//     same defect class as N1 — the platform templates (admin write surface,
//     Writable=false app page, read-only empty body) and the import set with
//     neither time nor the api package.
var exampleResourceFixtures = []struct {
	module   string
	mutation CreateMutation
}{
	{
		module:   "example-resource",
		mutation: CreateMutation{Name: "example-resource", Scope: "org", API: true, Admin: true, Search: true},
	},
	{
		module:   "example-feed",
		mutation: CreateMutation{Name: "example-feed", Scope: "platform", API: true, NoUI: true},
	},
	{
		module:   "example-notice",
		mutation: CreateMutation{Name: "example-notice", Scope: "platform"},
	},
}

// The two fixtures must not overlap on anything the registry treats as a
// global name, or the second one would refuse to install beside the first
// rather than exercising anything. Checked here because the collision would
// otherwise surface deep inside `registry validate` as a preflight refusal.
func TestExampleResourceFixturesDoNotCollide(t *testing.T) {
	seen := map[string]string{}
	claim := func(t *testing.T, kind, value, owner string) {
		t.Helper()
		key := kind + " " + value
		if previous, taken := seen[key]; taken {
			t.Fatalf("%s is claimed by both %s and %s", key, previous, owner)
		}
		seen[key] = owner
	}
	for _, fixture := range exampleResourceFixtures {
		_, manifest := buildResource(t, fixture.mutation)
		claim(t, "module", manifest.ID, fixture.module)
		for _, table := range manifest.Claims.Data {
			claim(t, "table", table, fixture.module)
		}
		for _, query := range manifest.Claims.Queries {
			claim(t, "query", query, fixture.module)
		}
		for _, route := range manifest.Runtime.Routes {
			claim(t, "route", route.ID, fixture.module)
			claim(t, "pattern", route.Method+" "+route.Pattern, fixture.module)
			claim(t, "handler", route.Handler, fixture.module)
		}
		for _, entry := range manifest.Runtime.Navigation {
			claim(t, "navigation", entry.ID, fixture.module)
		}
		for _, item := range manifest.Runtime.Visual {
			claim(t, "visual", item.ID, fixture.module)
		}
		for key := range manifest.Locales["en"] {
			claim(t, "i18n", key, fixture.module)
		}
		for _, migration := range manifest.Migrations {
			claim(t, "migration", migration.ID, fixture.module)
		}
		if manifest.OpenAPI != nil {
			for _, tag := range manifest.OpenAPI.Tags {
				claim(t, "openapi tag", tag.Name, fixture.module)
			}
			for name := range manifest.OpenAPI.Components["schemas"] {
				claim(t, "openapi schema", name, fixture.module)
			}
			for _, operation := range manifest.OpenAPI.Operations {
				claim(t, "openapi operation", operation.OperationID, fixture.module)
			}
		}
	}
}

// The platform-with-UI fixture exists for the branches the other two leave
// uncompiled, so those are what it pins.
func TestExampleNoticeFixtureIsThePlatformUIShape(t *testing.T) {
	files, manifest := buildResource(t, exampleResourceFixtures[2].mutation)
	const dir = resourceFixtureRegistry + "/registry/modules/workflow/example-notice"
	transport := string(files[dir+"/payload/workflow_example_notice.go.txt"])

	// The empty audit organization: a global change must not land in the
	// acting staff member's own tenant feed.
	for _, action := range []string{"created", "updated", "deleted"} {
		want := `s.logAudit(ctx, "", user.UserID, "example_notice.` + action + `"`
		if !strings.Contains(transport, want) {
			t.Fatalf("the fixture does not compile the empty audit organization: missing %q", want)
		}
	}
	if strings.Contains(transport, "org.OrgID") || strings.Contains(transport, "identity.OrgFrom") {
		t.Fatal("a platform mutation still reads an organization")
	}

	// The bare single-argument sqlc calls, which only a table with no tenant
	// predicate produces and which no other fixture reaches.
	for _, want := range []string{
		"s.q.GetExampleNoticeByID(ctx, id)",
		"s.q.CreateExampleNotice(ctx, name)",
		"s.q.DeleteExampleNotice(ctx, id)",
		"s.q.CountExampleNotices(ctx, query)",
	} {
		if !strings.Contains(transport, want) {
			t.Fatalf("the fixture does not compile the bare sqlc form %q:\n%s", want, transport)
		}
	}

	// The import set with neither time nor the api package.
	for _, absent := range []string{`"time"`, "internal/api", "encoding/json"} {
		if strings.Contains(transport, absent) {
			t.Fatalf("the no-api import branch is not exercised: transport still imports %s", absent)
		}
	}
	if manifest.OpenAPI != nil {
		t.Fatal("a resource without --api declared an OpenAPI slice")
	}

	// The platform templates: the app page is the read surface, the staff page
	// is the write surface, and the empty state has both bodies.
	templ := string(files[dir+"/payload/example-notice.templ.txt"])
	for _, want := range []string{
		"templ AdminExampleNoticesPage(",
		"func exampleNoticesWritable(ctx context.Context, d ExampleNoticesListData) bool {",
		"func exampleNoticesEmptyBody(ctx context.Context, d ExampleNoticesListData) string {",
	} {
		if !strings.Contains(templ, want) {
			t.Fatalf("the platform templates are missing %q", want)
		}
	}
	if !strings.Contains(transport, "Writable:   false") || !strings.Contains(transport, "Writable:   true") {
		t.Fatalf("the fixture does not compile both surfaces: one read-only, one writable:\n%s", transport)
	}
	if !slices.Equal(manifest.Tests.GoPackages, []string{"internal/web"}) {
		t.Fatalf("tests.go_packages = %v, want internal/web: the shape emits a validator to test",
			manifest.Tests.GoPackages)
	}
	// The emitted test must be reachable by the validator's -run ^TestExample.
	unit := string(files[dir+"/payload/workflow_example_notice_test.go.txt"])
	if !strings.Contains(unit, "func TestExampleNoticeNameValidation(t *testing.T) {") {
		t.Fatalf("the emitted test is not named for the validator's filter:\n%s", unit)
	}
}

// The narrowed fixture is the one whose declaration set the AST check cannot
// fully prove, so what makes it worth compiling is pinned here.
func TestExampleFeedFixtureIsTheNarrowedShape(t *testing.T) {
	files, manifest := buildResource(t, exampleResourceFixtures[1].mutation)

	if got := targets(manifest); !slices.Equal(got, []string{
		"internal/db/queries/example_feeds.sql", "internal/web/workflow_example_feed.go",
	}) {
		t.Fatalf("targets = %v, want only the query file and the transport", got)
	}
	if len(manifest.Tests.GoPackages) != 0 {
		t.Fatalf("tests.go_packages = %v, want none: the shape emits no validator to test",
			manifest.Tests.GoPackages)
	}
	if got := routeIDs(manifest); !slices.Equal(got, []string{"api.example-feed.list"}) {
		t.Fatalf("routes = %v, want only the read route", got)
	}
	if len(manifest.Dependencies.Go) != 0 {
		t.Fatalf("dependencies.go = %+v, want none: the shape imports neither pgx nor testify",
			manifest.Dependencies.Go)
	}
	if slices.Contains(requirementIDs(manifest), "ggg/system/identity") {
		t.Fatalf("requires = %v, want no identity: the shape reads no identity", requirementIDs(manifest))
	}
	if len(manifest.Locales) != 0 || len(manifest.Runtime.Navigation) != 0 || len(manifest.Runtime.Visual) != 0 {
		t.Fatal("--no-ui left a browser declaration behind")
	}
	transport := string(files[resourceFixtureRegistry+
		"/registry/modules/workflow/example-feed/payload/workflow_example_feed.go.txt"])
	for _, absent := range []string{"identity.", "pgx.", "logAudit", "validateExampleFeedName", "templates."} {
		if strings.Contains(transport, absent) {
			t.Fatalf("the narrowed transport still mentions %q:\n%s", absent, transport)
		}
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
