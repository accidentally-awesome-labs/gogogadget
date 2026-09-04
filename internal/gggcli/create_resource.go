package gggcli

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"unicode"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// resourceSpec is one fully resolved `ggg create resource` invocation. Every
// name the emitted slice uses is derived here, once, so the manifest
// declarations and the source that implements them cannot disagree about a
// route id, a table, a query name or an i18n key.
type resourceSpec struct {
	namespace string
	// slug is the kebab identity: the module name, the i18n prefix, the DOM id
	// prefix and the visual baseline stem.
	slug string
	// snake is the singular snake identity used for Go file names, audit
	// actions and the migration id.
	snake string
	// exported is the singular Go identifier and exports its plural. Both
	// appear verbatim in handler, template and sqlc method names.
	exported string
	exports  string
	// lower is the unexported singular Go identifier and lowers its plural.
	lower  string
	lowers string
	// humanSingular/humanPlural are the display words in lower case; the
	// Human* forms are sentence-cased for titles.
	humanSingular string
	humanPlural   string
	HumanSingular string
	HumanPlural   string
	// table is the Postgres table: plural snake unless --table overrides it.
	table string
	// route is the app surface prefix and plural the kebab plural used by the
	// admin and API paths.
	route  string
	plural string
	// scope is user, org or platform. tenantColumn is empty for platform: a
	// platform table has no tenant to filter by, which is what changes the
	// emitted SQL and the sqlc call arity.
	scope        string
	tenantColumn string

	api    bool
	admin  bool
	search bool
	noUI   bool
}

// The surface predicates. Every one of them is read by both the declaration
// builders and the emitted bodies, so a route the manifest declares and the
// handler that serves it cannot disagree about whether it exists.

// uiRead reports whether the browser read page is emitted; uiWrite reports
// whether the browser mutations are. They move together: a UI with no way to
// create a row is not the slice this command promises.
func (r resourceSpec) uiRead() bool  { return !r.noUI }
func (r resourceSpec) uiWrite() bool { return !r.noUI }

// apiWrite is narrower than apiRead. An API token carries an organization and
// nothing else, so on a platform table — which has no tenant at all — a JSON
// write would let any token holder rewrite global state with no staff check
// and no route scope that could impose one. The read is exactly what
// "platform" means, so it stays.
func (r resourceSpec) apiRead() bool  { return r.api }
func (r resourceSpec) apiWrite() bool { return r.api && r.tenantColumn != "" }

// needsValidator covers both transports; needsFormInput covers only the HTML
// one, so `--no-ui --api` does not carry a form:"name" struct that nothing
// decodes. The emitted unit test exercises the validator, so it travels with
// the validator rather than with the handlers: a shape that serves requests
// but writes nothing has no validator for that test to call.
func (r resourceSpec) needsValidator() bool { return r.uiWrite() || r.apiWrite() }
func (r resourceSpec) needsFormInput() bool { return r.uiWrite() }

// usesIdentity is read by the import list AND by the module requirement, which
// is the point: two gates on one predicate cannot drift into declaring a
// dependency the source never imports.
func (r resourceSpec) usesIdentity() bool {
	return r.uiWrite() || r.apiWrite() || (r.apiRead() && r.tenantColumn != "")
}

// hasHandlers reports whether any request handler is emitted.
func (r resourceSpec) hasHandlers() bool { return r.uiRead() || r.apiRead() }

// hasGo reports whether there is any Go file to emit at all. A
// queries-and-migration-only resource (--no-ui, no --api, no --search) has none.
func (r resourceSpec) hasGo() bool { return r.hasHandlers() || r.search }

// needsRowLookup reports whether a body distinguishes "no such row" from an
// error, which is what pulls in errors and pgx.
func (r resourceSpec) needsRowLookup() bool { return r.uiRead() || r.apiWrite() }

// writeBase is the URL prefix the mutations hang off, and writeScope/
// writePolicy the middleware group that guards them. A tenant-scoped table is
// written by its owner from the app group; a platform table has no owner, so
// its mutations belong to staff — the app group is requireAuth →
// requireNotDisabled → requireOrg → loadPlan with no requireStaff, and putting
// a global table behind it would hand every authenticated member of every
// organization create, rename and delete on it.
func (r resourceSpec) writeBase() string {
	if r.tenantColumn == "" {
		return "/admin/" + r.plural
	}
	return r.route
}

func (r resourceSpec) writeScope() modkit.RouteScope {
	if r.tenantColumn == "" {
		return modkit.RouteAdmin
	}
	return modkit.RouteApp
}

func (r resourceSpec) writePolicy() modkit.RoutePolicy {
	if r.tenantColumn == "" {
		return modkit.RoutePolicy{AdminWrite: true}
	}
	return modkit.RoutePolicy{}
}

// writeLayout is the shell the mutation handlers re-render a rejected form in.
func (r resourceSpec) writeLayout() string {
	if r.tenantColumn == "" {
		return "templates.LayoutAdmin"
	}
	return "templates.LayoutApp"
}

// adminUsesAllRows reports whether the admin surface needs its own untenanted
// queries. A tenant-scoped table does: a staff surface filtered to the
// operator's own tenant would not be a staff surface. A platform table already
// has no filter, so the ordinary list serves both.
func (r resourceSpec) adminUsesAllRows() bool { return r.admin && r.tenantColumn != "" }

// resolveResourceSpec validates the flags and derives every name. It refuses
// before a single file is built, so an impossible combination never reaches the
// plan.
func resolveResourceSpec(namespace, name string, mutation CreateMutation) (resourceSpec, error) {
	if mutation.Scope != "user" && mutation.Scope != "org" && mutation.Scope != "platform" {
		return resourceSpec{}, usageError("create resource --scope must be user, org, or platform")
	}
	// An admin surface IS a UI surface. Accepting both flags would either emit
	// the templates --no-ui promised to omit or declare admin routes whose
	// renderer does not exist; saying so is better than either.
	if mutation.NoUI && mutation.Admin {
		return resourceSpec{}, usageError(
			"create resource --no-ui cannot be combined with --admin: the admin surface is a UI surface")
	}
	// RequireAPIToken puts an organization in the context and nothing else, so
	// a user-scoped table has no tenant on the JSON transport. Emitting it
	// would list every user's rows to any token holder.
	if mutation.API && mutation.Scope == "user" {
		return resourceSpec{}, usageError(
			"create resource --api requires --scope org or platform: API tokens are organization-scoped and carry no user")
	}
	// The search index is organization-scoped: search_documents.tenant_id has
	// a foreign key to orgs, so every document must belong to one
	// organization. A platform row belongs to none, and filing it under
	// whichever organization's staff member happened to write it would both
	// misattribute it and hide it from every other organization.
	if mutation.Search && mutation.Scope == "platform" {
		return resourceSpec{}, usageError(
			"create resource --search requires --scope user or org: " +
				"the search index is organization-scoped and a platform table has no owning organization")
	}
	slug := kebab(name)
	if slug == "" {
		return resourceSpec{}, usageError("create name must contain letters or digits")
	}
	spec := resourceSpec{
		namespace: namespace,
		slug:      slug,
		snake:     snake(name),
		exported:  exported(name),
		lower:     unexported(name),
		table:     mutation.Table,
		route:     mutation.Route,
		plural:    slug + "s",
		scope:     mutation.Scope,
		api:       mutation.API,
		admin:     mutation.Admin,
		search:    mutation.Search,
		noUI:      mutation.NoUI,
	}
	spec.exports = spec.exported + "s"
	spec.lowers = spec.lower + "s"
	spec.humanSingular = strings.ToLower(titleWords(name))
	spec.humanPlural = spec.humanSingular + "s"
	spec.HumanSingular = sentenceCase(spec.humanSingular)
	spec.HumanPlural = sentenceCase(spec.humanPlural)
	if spec.table == "" {
		spec.table = spec.snake + "s"
	}
	if spec.route == "" {
		spec.route = "/app/" + spec.plural
	}
	switch spec.scope {
	case "org":
		spec.tenantColumn = "org_id"
	case "user":
		spec.tenantColumn = "user_id"
	}
	// A platform table's mutations live at admin scope, so the staff page that
	// renders them is not optional — it is the write surface. --admin is
	// already implied there rather than something the operator can forget.
	if spec.tenantColumn == "" && spec.uiRead() {
		spec.admin = true
	}
	return spec, nil
}

// expand substitutes the spec's names into one template body.
func (r resourceSpec) expand(body string) string {
	return strings.NewReplacer(
		"$Exps$", r.exports,
		"$Exp$", r.exported,
		"$lows$", r.lowers,
		"$low$", r.lower,
		"$snake$", r.snake,
		"$slug$", r.slug,
		"$table$", r.table,
		"$tcol$", r.tenantColumn,
		"$writeBase$", r.writeBase(),
		"$route$", r.route,
		"$plural$", r.plural,
		"$HumanSingular$", r.HumanSingular,
		"$HumanPlural$", r.HumanPlural,
		"$humanSingular$", r.humanSingular,
		"$humanPlural$", r.humanPlural,
	).Replace(body)
}

// resourceSlice is everything one resolved invocation produces: the module,
// its payloads keyed by installed target, its migration payloads keyed by
// registry source, and the diagnostics the operator should see on the plan.
type resourceSlice struct {
	manifest    modkit.Manifest
	payloads    map[string][]byte
	migrations  map[string][]byte
	diagnostics []modkit.Diagnostic
}

// buildResourceModule renders one complete resource slice: the sqlc query file,
// the immutable migration, the transport, the templates, the test, and every
// declaration that binds them into the generated route table, navigation,
// locale catalogs, visual matrix, OpenAPI document and data ledger.
func buildResourceModule(
	registry modkit.ProjectRegistry, kind modkit.ModuleKind, name string, mutation CreateMutation, base modkit.Manifest,
) (resourceSlice, error) {
	spec, err := resolveResourceSpec(registry.Namespace, name, mutation)
	if err != nil {
		return resourceSlice{manifest: base}, err
	}
	manifest := base
	manifest.Title = spec.HumanPlural
	// The description names what this invocation actually emitted. A --no-ui
	// slice that advertises templates is a manifest describing a different
	// module, and `ggg catalog`/`ggg info` print it verbatim.
	parts := []string{"queries", "migration"}
	if spec.hasGo() {
		parts = append(parts, "transport")
	}
	if spec.uiRead() {
		parts = append(parts, "templates")
	}
	parts = append(parts, "declarations")
	manifest.Description = "The project-local " + spec.humanSingular + " slice: the " + spec.table +
		" table with its " + joinWords(parts) + "."

	payloads := map[string][]byte{
		"internal/db/queries/" + spec.table + ".sql": []byte(spec.queriesSQL()),
	}
	if spec.hasGo() {
		payloads["internal/web/workflow_"+spec.snake+".go"] = []byte(spec.transportGo())
	}
	if spec.needsValidator() {
		payloads["internal/web/workflow_"+spec.snake+"_test.go"] = []byte(spec.transportTestGo())
	}
	if !spec.noUI {
		payloads["internal/web/templates/"+spec.slug+".templ"] = []byte(spec.templatesTempl())
	}

	// The migration enters the manifest ledger rather than the file list: the
	// allocator assigns its installed %04d_ sequence at sync, goose can parse
	// it, and removal knows the schema payload belongs to this module.
	migrationBody := []byte(spec.migrationSQL())
	migrationSum := sha256.Sum256(migrationBody)
	migrationSource := "registry/modules/" + string(kind) + "/" + spec.slug + "/payload/" + spec.snake + ".sql"
	manifest.Migrations = []modkit.ManifestMigration{{
		ID: spec.namespace + "." + spec.snake, Kind: modkit.MigrationImmutable,
		Source: migrationSource, SHA256: hex.EncodeToString(migrationSum[:]),
	}}

	manifest.Requires = spec.requirements()
	manifest.Dependencies = spec.dependencies()
	manifest.Runtime.Routes = spec.routes()
	manifest.Runtime.Queries = spec.queries()
	if !spec.noUI {
		manifest.Runtime.Navigation = spec.navigation()
		manifest.Runtime.Visual = spec.visual()
		manifest.Locales = spec.locales()
	}
	if spec.api {
		manifest.OpenAPI = spec.openapi()
	}
	manifest.Data = spec.data()
	manifest.Tests = spec.tests()
	manifest.Claims = spec.claims()

	return resourceSlice{
		manifest:    manifest,
		payloads:    payloads,
		migrations:  map[string][]byte{migrationSource: migrationBody},
		diagnostics: spec.diagnostics(),
	}, nil
}

// diagnostics reports what the flags asked for and the slice deliberately did
// not emit. A narrowing that is right but invisible is a narrowing the
// operator discovers from a 404, so it is said on the plan instead.
func (r resourceSpec) diagnostics() []modkit.Diagnostic {
	out := make([]modkit.Diagnostic, 0, 1)
	if r.api && !r.apiWrite() {
		out = append(out, modkit.Diagnostic{
			Code: "resource_api_read_only", Severity: "info",
			Module: r.namespace + "/workflow/" + r.slug,
			Path:   "/api/v1/" + r.plural,
			Message: "--api on a platform-scoped resource emits the read route only; " +
				"create, update and delete are omitted because a token-authenticated write " +
				"has no tenant predicate and no route scope that can require staff. " +
				"The mutations are served from /admin/" + r.plural + ".",
		})
	}
	return out
}

// coreContractMaxima is the highest contract of a core module that this
// generator's emitted source is known to work with. Everything it emits
// predates every core contract bump so far, so the ranges start at 1 and
// stretch to whatever is published now; a module generated here must resolve
// against the catalog it was generated from, and a range of exactly [1,1]
// refused the moment ggg/system/server moved to 2.
//
// TestGeneratedRequirementsCoverCoreContracts holds this against the core
// registry, so a future bump fails here rather than in a generated project.
var coreContractMaxima = map[string]int{
	"ggg/system/server": 2,
}

// requirements names every module the emitted source actually imports or
// depends on. A resource that reaches identity, i18n and the ui package but
// declares only the database is a module that compiles on the machine that
// authored it and nowhere else.
func (r resourceSpec) requirements() []modkit.Requirement {
	ids := []string{"ggg/system/database"}
	if r.hasHandlers() {
		// Any route at all needs the mux and the middleware chain.
		ids = append(ids, "ggg/system/security", "ggg/system/server")
	}
	if r.usesIdentity() {
		ids = append(ids, "ggg/system/identity")
	}
	if !r.noUI {
		ids = append(ids, "ggg/system/i18n")
	}
	if r.scope == "org" {
		ids = append(ids, "ggg/system/organizations")
	}
	if r.api {
		ids = append(ids, "ggg/system/api", "ggg/workflow/openapi-contract")
	}
	if r.search {
		ids = append(ids, "ggg/system/search")
	}
	seen := map[string]struct{}{}
	out := make([]modkit.Requirement, 0, len(ids))
	for _, id := range ids {
		if _, already := seen[id]; already {
			continue
		}
		seen[id] = struct{}{}
		bounds := modkit.ContractBounds{Min: 1, Max: 1}
		if published, ok := coreContractMaxima[id]; ok && published > bounds.Max {
			bounds.Max = published
		}
		out = append(out, modkit.Requirement{ID: id, Contract: bounds})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r resourceSpec) dependencies() modkit.Dependencies {
	deps := emptyDependencies()
	if r.needsRowLookup() {
		// pgx.ErrNoRows is the 404 discriminator in every row lookup — the
		// same predicate the import list uses, so the declaration cannot name
		// a module the source never imports.
		deps.Go = []modkit.GoDependency{{Module: "github.com/jackc/pgx/v5", Version: pgxDependencyVersion}}
	}
	return deps
}

// apiRoutePolicy is the cookieless transport policy. CSRF is exempt because
// the caller is a Bearer token rather than a browser session, and a write body
// is capped well below the global limit.
func apiRoutePolicy(write bool) modkit.RoutePolicy {
	policy := modkit.RoutePolicy{
		CSRFExempt: true,
		CSRFReason: "cookieless transport: authenticated by Bearer token, not a browser session",
	}
	if write {
		policy.MaxBodyBytes = 65536
	}
	return policy
}

func (r resourceSpec) routes() []modkit.RouteContribution {
	const pkg = "internal/web"
	out := make([]modkit.RouteContribution, 0, 9)
	if r.uiRead() {
		out = append(out, modkit.RouteContribution{ID: r.slug + ".index", Method: "GET", Pattern: r.route,
			Scope: modkit.RouteApp, Package: pkg, Handler: "handle" + r.exports})
	}
	if r.uiWrite() {
		// writeScope/writePolicy are what keep a platform table's mutations
		// inside the admin group with the write-role check; a tenant-scoped
		// table's stay in the app group, guarded by its own tenant predicate.
		base, scope, policy := r.writeBase(), r.writeScope(), r.writePolicy()
		out = append(out,
			modkit.RouteContribution{ID: r.slug + ".create", Method: "POST", Pattern: base,
				Scope: scope, Policy: policy, Package: pkg, Handler: "handle" + r.exported + "Create"},
			modkit.RouteContribution{ID: r.slug + ".update", Method: "POST", Pattern: base + "/{id}",
				Scope: scope, Policy: policy, Package: pkg, Handler: "handle" + r.exported + "Update"},
			modkit.RouteContribution{ID: r.slug + ".delete", Method: "DELETE", Pattern: base + "/{id}",
				Scope: scope, Policy: policy, Package: pkg, Handler: "handle" + r.exported + "Delete"},
		)
	}
	if r.admin {
		out = append(out, modkit.RouteContribution{ID: r.slug + ".admin", Method: "GET", Pattern: "/admin/" + r.plural,
			Scope: modkit.RouteAdmin, Package: pkg, Handler: "handleAdmin" + r.exports})
	}
	if r.apiRead() {
		out = append(out, modkit.RouteContribution{ID: "api." + r.slug + ".list", Method: "GET",
			Pattern: "/api/v1/" + r.plural, Scope: modkit.RouteAPIRead, Policy: apiRoutePolicy(false),
			Package: pkg, Handler: "handleAPIList" + r.exports})
	}
	if r.apiWrite() {
		base := "/api/v1/" + r.plural
		out = append(out,
			modkit.RouteContribution{ID: "api." + r.slug + ".create", Method: "POST", Pattern: base,
				Scope: modkit.RouteAPIWrite, Policy: apiRoutePolicy(true), Package: pkg,
				Handler: "handleAPICreate" + r.exported},
			modkit.RouteContribution{ID: "api." + r.slug + ".update", Method: "PATCH", Pattern: base + "/{id}",
				Scope: modkit.RouteAPIWrite, Policy: apiRoutePolicy(true), Package: pkg,
				Handler: "handleAPIUpdate" + r.exported},
			modkit.RouteContribution{ID: "api." + r.slug + ".delete", Method: "DELETE", Pattern: base + "/{id}",
				Scope: modkit.RouteAPIWrite, Policy: apiRoutePolicy(false), Package: pkg,
				Handler: "handleAPIDelete" + r.exported},
		)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// queryNames is the ordered sqlc method set the slice owns, matching the
// `-- name:` annotations in the emitted query file exactly.
func (r resourceSpec) queryNames() []string {
	names := []string{
		"Count" + r.exports,
		"Create" + r.exported,
		"Delete" + r.exported,
		"Get" + r.exported + "ByID",
		"List" + r.exports,
		"Update" + r.exported,
	}
	if r.adminUsesAllRows() {
		names = append(names, "CountAll"+r.exports, "ListAll"+r.exports)
	}
	sort.Strings(names)
	return names
}

func (r resourceSpec) queries() []modkit.QueryContribution {
	names := r.queryNames()
	out := make([]modkit.QueryContribution, 0, len(names))
	for _, name := range names {
		out = append(out, modkit.QueryContribution{Name: name, Table: r.table})
	}
	return out
}

func (r resourceSpec) navigation() []modkit.NavigationContribution {
	out := []modkit.NavigationContribution{{
		ID: "nav.app." + r.slug, Area: modkit.NavAreaApp, RouteID: r.slug + ".index", LabelKey: r.slug + ".nav",
	}}
	if r.admin {
		out = append(out, modkit.NavigationContribution{
			ID: "nav.admin." + r.slug, Area: modkit.NavAreaAdmin,
			RouteID: r.slug + ".admin", LabelKey: r.slug + ".admin_nav",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (r resourceSpec) visual() []modkit.VisualContribution {
	return []modkit.VisualContribution{{
		ID: r.slug, Path: r.route, Persona: "pro",
		Viewports: []string{"desktop", "tablet", "mobile"},
		Masks:     []string{"[data-testid=\"relative-time\"]"},
	}}
}

// data declares the lifecycle obligations of the new table. A tenant-scoped
// table is created with an ON DELETE CASCADE foreign key, so cascade is what
// the schema actually does; a platform table has no owner and retains. Export
// is false because nothing has written a redaction DTO for these columns yet,
// and claiming exportability without a collector is exactly what the generated
// lifecycle registry exists to catch.
func (r resourceSpec) data() []modkit.DataDeclaration {
	declaration := modkit.DataDeclaration{
		Table: r.table, Scope: modkit.DataScope(r.scope), Export: false,
		AccountDelete: modkit.DeleteRetain, OrganizationDelete: modkit.DeleteRetain,
	}
	switch r.scope {
	case "org":
		// Deleting an account takes its sole-member organizations with it, and
		// the organization cascade then reaches these rows.
		declaration.AccountDelete = modkit.DeleteCascade
		declaration.OrganizationDelete = modkit.DeleteCascade
	case "user":
		declaration.AccountDelete = modkit.DeleteCascade
	}
	return []modkit.DataDeclaration{declaration}
}

// tests declares internal/web only when the slice actually contributes a test
// to it, which is exactly when the validator — the one piece of pure logic
// here — is emitted.
func (r resourceSpec) tests() modkit.TestMetadata {
	if !r.needsValidator() {
		return modkit.TestMetadata{}
	}
	return modkit.TestMetadata{GoPackages: []string{"internal/web"}}
}

func (r resourceSpec) claims() modkit.NamespaceClaims {
	claims := modkit.NamespaceClaims{Queries: r.queryNames(), Data: []string{r.table}}
	for _, route := range r.routes() {
		claims.Routes = append(claims.Routes, route.ID)
	}
	return claims
}

// unexported is exported() with a lower-case first rune: the unexported Go
// identifier for one resource.
func unexported(value string) string {
	name := exported(value)
	runes := []rune(name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// joinWords renders a list as prose: "a, b and c".
func joinWords(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// sentenceCase upper-cases the first rune and leaves the rest alone, which is
// what a title needs from a lower-case display phrase.
func sentenceCase(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
