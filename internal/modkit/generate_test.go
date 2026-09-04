package modkit

import (
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func genFixtureLock(t *testing.T) (Lock, []Manifest) {
	t.Helper()
	config := Manifest{
		ID: "ggg/system/config", Kind: ModuleSystem, Name: "config",
		Revision: 1, Contract: 1, Title: "Config", Description: "Config root.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{System: &SystemContribution{
			Package: "internal/config", Constructor: "NewModule",
			Needs: []RuntimeNeed{}, Provides: []RuntimeProvide{{Field: "Config", Capability: "config", Type: "*config.Config"}},
		}},
	}
	system := Manifest{
		ID: "ggg/system/alpha", Kind: ModuleSystem, Name: "alpha",
		Revision: 1, Contract: 1, Title: "Alpha", Description: "Alpha system.",
		Files:         []ManifestFile{},
		Requires:      []Requirement{{ID: "ggg/system/config", Contract: ContractBounds{Min: 1, Max: 1}}},
		RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{
			System: &SystemContribution{
				Package:     "internal/alpha",
				Constructor: "New",
				Needs: []RuntimeNeed{
					{Field: "Config", Capability: "config", Type: "*config.Config"},
				},
				Provides: []RuntimeProvide{
					{Field: "Alpha", Capability: "alpha", Type: "*alpha.Module"},
				},
				Start: true, Stop: true,
			},
		},
	}
	page := Manifest{
		ID: "ggg/page/beta", Kind: ModulePage, Name: "beta",
		Revision: 1, Contract: 1, Title: "Beta", Description: "Beta page.",
		Files:         []ManifestFile{},
		Requires:      []Requirement{{ID: "ggg/system/alpha", Contract: ContractBounds{Min: 1, Max: 1}}},
		RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{
			Routes: []RouteContribution{
				{ID: "beta.show", Method: "GET", Pattern: "/beta", Scope: RoutePublic,
					Policy: RoutePolicy{}, Package: "internal/beta", Handler: "Show"},
			},
		},
	}
	lock := Lock{
		Schema:         2,
		RegistryCommit: testCommitA,
		Order:          []string{"ggg/system/config", "ggg/system/alpha", "ggg/page/beta"},
		Modules: []LockedModule{
			{ID: "ggg/system/config", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{"ggg/system/alpha"}, Manifest: config, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/system/alpha", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{"ggg/page/beta"}, Manifest: system, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/page/beta", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: page, Files: []LockedFile{}, Migrations: []LockedMigration{}},
		},
	}
	return lock, []Manifest{config, system, page}
}

// Two modules contributing the same command name cannot share the one
// dispatch table: generation refuses rather than emitting a registry where
// one declaration silently shadows the other.
func TestCommandsRegistryRefusesDuplicateNames(t *testing.T) {
	cli := CLIContribution{Name: "ui", Summary: "console", Package: "internal/console", Handler: "Run"}
	one := Manifest{ID: "ggg/system/one", Kind: ModuleSystem, Name: "one", Runtime: RuntimeContributions{CLI: []CLIContribution{cli}}}
	two := Manifest{ID: "ggg/system/two", Kind: ModuleSystem, Name: "two", Runtime: RuntimeContributions{CLI: []CLIContribution{cli}}}
	lock := Lock{Schema: 2, RegistryCommit: testCommitA, Order: []string{one.ID, two.ID}}
	if _, err := emitCommandsRegistry(context.Background(), "example.com/acme", lock, []Manifest{one, two}); err == nil ||
		!strings.Contains(err.Error(), `contributed command "ui" is declared by both`) {
		t.Fatalf("duplicate contributed name = %v, want generation refusal", err)
	}
}

// The command registry renders into the leaf commands package: one entry per
// declaration, canonical order, so cmd/ggg never imports internal/modules.
func TestCommandsRegistryRendersLeafPackage(t *testing.T) {
	module := Manifest{ID: "ggg/system/console", Kind: ModuleSystem, Name: "console", Runtime: RuntimeContributions{
		CLI: []CLIContribution{{Name: "ui", Summary: "Open the interactive console", Package: "internal/gggcli/ui", Handler: "Run"}},
	}}
	lock := Lock{Schema: 2, RegistryCommit: testCommitA, Order: []string{module.ID}}
	file, err := emitCommandsRegistry(context.Background(), "example.com/acme", lock, []Manifest{module})
	if err != nil {
		t.Fatalf("emitCommandsRegistry: %v", err)
	}
	if file.Path != "internal/gggcli/commands/commands_registry_gen.go" {
		t.Fatalf("path = %s", file.Path)
	}
	for _, want := range []string{"package commands", "func CLICommands() []gggcli.ContributedCommand", `Name: "ui"`, "ui.Run"} {
		if !strings.Contains(file.Content, want) {
			t.Fatalf("registry missing %q:\n%s", want, file.Content)
		}
	}
	if strings.Contains(file.Content, "internal/modules") {
		t.Fatalf("registry references internal/modules:\n%s", file.Content)
	}
}

func TestGenerateAllIsDeterministic(t *testing.T) {
	lock, graph := genFixtureLock(t)
	a, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	b, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll(second): %v", err)
	}
	if len(a) == 0 {
		t.Fatal("GenerateAll produced no files")
	}
	if len(a) != len(b) {
		t.Fatalf("file counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Content != b[i].Content {
			t.Fatalf("file %d differs: %s vs %s", i, a[i].Path, b[i].Path)
		}
	}
}

func TestBootstrapRejectsConstructorGraphWithoutConfig(t *testing.T) {
	module := Manifest{
		ID: "ggg/system/orphan", Kind: ModuleSystem, Name: "orphan",
		Revision: 1, Contract: 1, Title: "Orphan", Description: "Orphan.",
		Requires: []Requirement{}, Files: []ManifestFile{}, Migrations: []ManifestMigration{},
		Environment: []EnvironmentVariable{}, Docs: []DocumentationRef{}, Data: []DataDeclaration{},
		Tests: TestMetadata{}, Dependencies: Dependencies{Go: []GoDependency{}, Tools: []ToolArtifact{}, Containers: []ContainerDependency{}},
		RemovalPolicy: RemovalFree,
		Runtime:       RuntimeContributions{System: &SystemContribution{Package: "internal/orphan", Constructor: "New"}},
	}
	lock := Lock{Schema: 2, RegistryCommit: testCommitA, Order: []string{module.ID}, RuntimeOrders: RuntimeOrders{Development: []string{module.ID}, Test: []string{module.ID}, Production: []string{module.ID}}}
	if _, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{module}); err == nil || !strings.Contains(err.Error(), "config capability") {
		t.Fatalf("GenerateAll error = %v, want config capability refusal", err)
	}
}
func TestBootstrapCollapsedConstructorRemainsCompileSafe(t *testing.T) {
	lock, graph := genFixtureLock(t)
	graph[1].Runtime.System.Provides = []RuntimeProvide{}
	graph[1].Runtime.System.Start = false
	graph[1].Runtime.System.Stop = false
	lock.Modules[1].Manifest = graph[1]
	out, err := emitBootstrapRegistry(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("emitBootstrapRegistry: %v", err)
	}
	if !strings.Contains(out.Content, "_, err = alpha.New(ctx") {
		t.Fatalf("collapsed constructor assignment missing:\n%s", out.Content)
	}
}

// TestEveryEmittedPathIsRegistryOwned holds the emitters to the predicate that
// authorises sync to delete a stale aggregate. If an emitter grows a new output
// and nobody teaches IsRegistryOwnedOutputPath about it, the stale sweep would
// delete that file on every run.
func TestEveryEmittedPathIsRegistryOwned(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	for _, f := range files {
		if !IsRegistryOwnedOutputPath(f.Path) {
			t.Fatalf("generated file %s is not registry-owned; the stale sweep would delete it", f.Path)
		}
		if !IsGeneratedOutputPath(f.Path) {
			t.Fatalf("registry-owned path %s is not a generated output", f.Path)
		}
	}
}

// A failing generation has to be reproducible. Both of these iterated a map, so
// the operator fixed whatever came out first, reran, and got a different
// complaint — with no signal that more than one thing was wrong.
func TestGenerationErrorsAreDeterministic(t *testing.T) {
	t.Run("navigation area order", func(t *testing.T) {
		lock, graph := genFixtureLock(t)
		// Two areas, each with an entry ordered after an id that does not exist.
		// Whichever area is reported, it must be the same one every run.
		nav := []NavigationContribution{
			{ID: "beta.public", Area: NavAreaPublic, RouteID: "beta.show",
				LabelKey: "nav.beta", After: []string{"nowhere.public"}},
			{ID: "beta.app", Area: NavAreaApp, RouteID: "beta.show",
				LabelKey: "nav.beta", After: []string{"nowhere.app"}},
		}
		graph[1].Runtime.Navigation = nav
		lock.Modules[1].Manifest.Runtime.Navigation = nav

		first := ""
		for range 40 {
			_, err := resolveNavigation(lock, graph)
			if err == nil {
				t.Fatal("resolveNavigation accepted an entry ordered after an unknown id")
			}
			if first == "" {
				first = err.Error()
				continue
			}
			if err.Error() != first {
				t.Fatalf("navigation error is not reproducible:\n%s\n%s", first, err)
			}
		}
	})

	t.Run("undocumented API routes are all named", func(t *testing.T) {
		lock, graph := genFixtureLock(t)
		routes := []RouteContribution{
			{ID: "api.alpha", Method: "GET", Pattern: "/api/v1/alpha", Scope: RouteAPIRead,
				Policy: RoutePolicy{}, Package: "internal/beta", Handler: "Alpha"},
			{ID: "api.gamma", Method: "GET", Pattern: "/api/v1/gamma", Scope: RouteAPIRead,
				Policy: RoutePolicy{}, Package: "internal/beta", Handler: "Gamma"},
			{ID: "api.zeta", Method: "GET", Pattern: "/api/v1/zeta", Scope: RouteAPIRead,
				Policy: RoutePolicy{}, Package: "internal/beta", Handler: "Zeta"},
		}
		graph[1].Runtime.Routes = routes
		lock.Modules[1].Manifest.Runtime.Routes = routes
		// A module has to contribute an info block for the document to be built
		// at all; it documents none of the three routes.
		contribution := &OpenAPIContribution{
			Info: json.RawMessage(`{"title":"Fixture","version":"1"}`),
		}
		graph[1].OpenAPI = contribution
		lock.Modules[1].Manifest.OpenAPI = contribution

		first := ""
		for range 40 {
			_, err := buildOpenAPIDocument(lock, graph)
			if err == nil {
				t.Fatal("buildOpenAPIDocument accepted three undocumented API routes")
			}
			for _, id := range []string{"api.alpha", "api.gamma", "api.zeta"} {
				if !strings.Contains(err.Error(), id) {
					t.Fatalf("error does not name %s: %v", id, err)
				}
			}
			if !strings.Contains(err.Error(), "3 API route(s)") {
				t.Fatalf("error does not count the undocumented routes: %v", err)
			}
			if first == "" {
				first = err.Error()
				continue
			}
			if err.Error() != first {
				t.Fatalf("openapi error is not reproducible:\n%s\n%s", first, err)
			}
		}
	})
}

func TestBootstrapRegistryIsEmitted(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap, testhost *GeneratedFile
	for i := range files {
		switch files[i].Path {
		case "internal/modules/bootstrap_registry_gen.go":
			bootstrap = &files[i]
		case "internal/modules/testhost_registry_gen.go":
			testhost = &files[i]
		}
	}
	if bootstrap == nil || testhost == nil {
		t.Fatalf("missing bootstrap/testhost registry files")
	}
	if !strings.Contains(bootstrap.Content, "Code generated by ggg sync; DO NOT EDIT.") {
		t.Fatalf("bootstrap header missing")
	}
	if !strings.Contains(bootstrap.Content, "package modules") {
		t.Fatalf("bootstrap package missing")
	}
	// Deterministic index SHA must be embedded and stable.
	if !strings.Contains(bootstrap.Content, indexSHA(lock)) {
		t.Fatalf("bootstrap does not carry the lock index SHA %s", indexSHA(lock))
	}
}

func TestRoutesRegistryIsEmitted(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var routes *GeneratedFile
	for i := range files {
		if files[i].Path == "internal/web/routes_registry_gen.go" {
			routes = &files[i]
		}
	}
	if routes == nil {
		t.Fatalf("routes_registry_gen.go not generated")
	}
	for _, want := range []string{"package web", "beta.show", "/beta"} {
		if !strings.Contains(routes.Content, want) {
			t.Fatalf("routes registry missing %q", want)
		}
	}
}

func TestIndexSHAChangesWithLock(t *testing.T) {
	lock, graph := genFixtureLock(t)
	first := indexSHA(lock)
	lock.Modules[1].Revision = 2
	second := indexSHA(lock)
	if first == second {
		t.Fatalf("index SHA did not change with lock revision")
	}
	_ = graph
}

// Manifests are distributed to derivatives whose Go module path differs from the
// canonical one, so a manifest must never carry an absolute import path. Package
// fields are module-relative and the generator prefixes the target module path;
// otherwise every installed derivative would import the upstream module and fail
// to compile.
func TestGeneratedImportsUseTargetModulePath(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	if bootstrap == "" {
		t.Fatal("bootstrap registry was not emitted")
	}
	if want := `"derivative.example/app/internal/alpha"`; !strings.Contains(bootstrap, want) {
		t.Fatalf("bootstrap does not import %s:\n%s", want, bootstrap)
	}
	if strings.Contains(bootstrap, "example.com/acme/internal/alpha") {
		t.Fatalf("bootstrap leaked the canonical module path:\n%s", bootstrap)
	}
}

// Generated Go must parse, must be gofmt-clean, and must not declare unused
// variables. The pipeline runs inside a transaction that has already replaced
// authored files, so emitting source the compiler rejects would leave the tree
// broken until someone hand-edited a DO-NOT-EDIT file.
func TestGeneratedGoIsParseableAndFormatted(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	emitted := 0
	for _, file := range files {
		if !strings.HasSuffix(file.Path, ".go") {
			continue
		}
		emitted++
		formatted, err := format.Source([]byte(file.Content))
		if err != nil {
			t.Fatalf("%s does not parse: %v\n%s", file.Path, err, file.Content)
		}
		if string(formatted) != file.Content {
			t.Fatalf("%s is not gofmt-clean:\n--- emitted ---\n%s\n--- gofmt ---\n%s",
				file.Path, file.Content, formatted)
		}
	}
	if emitted == 0 {
		t.Fatal("no Go files were emitted")
	}
}

// Every capability a module provides must be reachable from the booted runtime.
// A constructor whose result is dropped is both dead wiring and a compile error.
func TestBootstrapExposesProvidedCapabilities(t *testing.T) {
	lock, graph := genFixtureLock(t)
	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	for _, m := range graph {
		if m.Runtime.System == nil {
			continue
		}
		for _, provide := range m.Runtime.System.Provides {
			field := capabilityField(provide.Capability)
			if !strings.Contains(bootstrap, "\t"+field+" ") {
				t.Fatalf("runtime has no field for capability %q (want %q):\n%s",
					provide.Capability, field, bootstrap)
			}
			if !strings.Contains(bootstrap, "r."+field+" = ") {
				t.Fatalf("capability %q is never assigned to the runtime:\n%s",
					provide.Capability, bootstrap)
			}
		}
	}
}

// An emitter must not render a table for a contribution kind nothing declares.
// The generated file would reference a type the project has no reason to define,
// turning an unused feature into a build break — and generated files are
// DO-NOT-EDIT, so the operator has no legitimate way to fix it.
func TestEmittersSkipUndeclaredContributionKinds(t *testing.T) {
	lock, graph := genFixtureLock(t)
	for i := range graph {
		graph[i].Runtime.Jobs = nil
		graph[i].Runtime.Routes = nil
		graph[i].Runtime.ContentTypes = nil
	}
	for i := range lock.Modules {
		lock.Modules[i].Manifest.Runtime.Jobs = nil
		lock.Modules[i].Manifest.Runtime.Routes = nil
		lock.Modules[i].Manifest.Runtime.ContentTypes = nil
	}

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	for _, file := range files {
		for _, forbidden := range []string{"jobs_registry_gen", "routes_registry_gen"} {
			if strings.Contains(file.Path, forbidden) {
				t.Fatalf("emitted %s with no matching contributions:\n%s", forbidden, file.Content)
			}
		}
	}
}

// A module that declares stop must actually be stopped, in reverse dependency
// order. A generated Close that discards its stoppers leaks pools and
// connections on every shutdown while looking correct.
func TestBootstrapClosesStoppableModulesInReverseOrder(t *testing.T) {
	lock, graph := genFixtureLock(t)
	// Give the fixture a second stoppable module so order is observable.
	second := Manifest{
		ID: "ggg/system/omega", Kind: ModuleSystem, Name: "omega",
		Revision: 1, Contract: 1, Title: "Omega", Description: "Omega system.",
		Files: []ManifestFile{}, Requires: []Requirement{{ID: "ggg/system/alpha", Contract: ContractBounds{Min: 1, Max: 1}}}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{System: &SystemContribution{
			Package: "internal/omega", Constructor: "NewModule",
			Needs:    []RuntimeNeed{{Field: "Alpha", Capability: "alpha", Type: "*alpha.Module"}},
			Provides: []RuntimeProvide{{Field: "Omega", Capability: "omega", Type: "*omega.Module"}},
			Stop:     true,
		}},
	}
	graph = append(graph, second)
	lock.Order = append(lock.Order, "ggg/system/omega")
	lock.Modules = append(lock.Modules, LockedModule{
		ID: "ggg/system/omega", Revision: 1, Contract: 1, SourceCommit: testCommitA,
		Reason: "explicit", RequiredBy: []string{}, Manifest: second,
		Files: []LockedFile{}, Migrations: []LockedMigration{},
	})

	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	if strings.Contains(bootstrap, "_ = system_") {
		t.Fatalf("Close discards its stoppers instead of calling them:\n%s", bootstrap)
	}

	// Stop hooks are registered during Boot, so dependency order is captured by
	// append order; Close then walks the slice backwards.
	bootBody := bootstrap[strings.Index(bootstrap, "func Boot("):strings.Index(bootstrap, "func (r *Runtime) Handler(")]
	alpha := strings.Index(bootBody, "r.stop = append(r.stop, ggg_system_alphaModule.Stop)")
	omega := strings.Index(bootBody, "r.stop = append(r.stop, ggg_system_omegaModule.Stop)")
	if alpha < 0 || omega < 0 {
		t.Fatalf("Boot does not register both stop hooks:\n%s", bootBody)
	}
	if alpha > omega {
		t.Fatalf("stop hooks registered out of dependency order:\n%s", bootBody)
	}

	closeBody := bootstrap[strings.Index(bootstrap, "func (r *Runtime) Close("):]
	if !strings.Contains(closeBody, "for i := len(r.stop) - 1; i >= 0; i--") {
		t.Fatalf("Close does not stop in reverse order:\n%s", closeBody)
	}
	if !strings.Contains(closeBody, "errors.Join(errs...)") {
		t.Fatalf("Close abandons later stop hooks after a failure:\n%s", closeBody)
	}
}

// A provided type can come from a package the providing module merely uses —
// a pool, a generated query struct. The bootstrap imports module packages, so
// without declared type imports the generated Runtime references undefined
// identifiers and the whole project stops compiling.
func TestBootstrapImportsDeclaredProvideTypePackages(t *testing.T) {
	lock, graph := genFixtureLock(t)
	for i := range graph {
		if graph[i].Runtime.System == nil {
			continue
		}
		graph[i].Runtime.System.TypeImports = []string{
			"github.com/jackc/pgx/v5/pgxpool",
			"internal/db/sqlc",
		}
		graph[i].Runtime.System.Provides = []RuntimeProvide{
			{Field: "Pool", Capability: "database.pool", Type: "*pgxpool.Pool"},
			{Field: "Queries", Capability: "database.queries", Type: "*sqlc.Queries"},
		}
	}
	for i := range lock.Modules {
		lock.Modules[i].Manifest = graph[i]
	}

	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	// An external module path is used verbatim; a module-relative one is
	// qualified against the target module, exactly like a system package.
	for _, want := range []string{
		`"github.com/jackc/pgx/v5/pgxpool"`,
		`"derivative.example/app/internal/db/sqlc"`,
	} {
		if !strings.Contains(bootstrap, want) {
			t.Fatalf("bootstrap does not import %s:\n%s", want, bootstrap)
		}
	}
}

// A module that declares start owns a long-lived service. The generated Run must
// actually start it: a Run that returns nil without starting anything means the
// job worker never claims a row while every test still passes.
func TestBootstrapRunsStartableModules(t *testing.T) {
	lock, graph := genFixtureLock(t)
	for i := range graph {
		if graph[i].Runtime.System != nil {
			graph[i].Runtime.System.Start = true
		}
	}
	for i := range lock.Modules {
		lock.Modules[i].Manifest = graph[i]
	}

	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	if !strings.Contains(bootstrap, "r.start = append(r.start, ggg_system_alphaModule.Start)") {
		t.Fatalf("Boot does not register the start hook:\n%s", bootstrap)
	}
	runBody := bootstrap[strings.Index(bootstrap, "func (r *Runtime) Run("):]
	runBody = runBody[:strings.Index(runBody, "\n}\n")]
	if !strings.Contains(runBody, "for _, start := range r.start") {
		t.Fatalf("Run does not start registered services:\n%s", runBody)
	}
	if !strings.Contains(runBody, "return err") {
		t.Fatalf("Run swallows a start failure:\n%s", runBody)
	}
}

// The runtime composes an HTTP handler, so one capability is structural: the
// module that provides http.handler is what Handler() returns. Without this the
// generated Handler() returns nil and the process serves nothing.
func TestBootstrapHandlerReturnsProvidedHTTPHandler(t *testing.T) {
	lock, graph := genFixtureLock(t)
	for i := range graph {
		if graph[i].Runtime.System == nil {
			continue
		}
		graph[i].Runtime.System.Provides = append(graph[i].Runtime.System.Provides,
			RuntimeProvide{Field: "Handler", Capability: "http.handler", Type: "http.Handler"})
	}
	for i := range lock.Modules {
		lock.Modules[i].Manifest = graph[i]
	}

	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var bootstrap string
	for _, file := range files {
		if file.Path == "internal/modules/bootstrap_registry_gen.go" {
			bootstrap = file.Content
		}
	}
	handlerBody := bootstrap[strings.Index(bootstrap, "func (r *Runtime) Handler("):]
	handlerBody = handlerBody[:strings.Index(handlerBody, "\n\n")]
	if !strings.Contains(handlerBody, "return r.HTTPHandler") {
		t.Fatalf("Handler() does not return the provided handler:\n%s", handlerBody)
	}
	if strings.Contains(bootstrap, "\thandler http.Handler\n") {
		t.Fatalf("runtime keeps an unassigned private handler field:\n%s", bootstrap)
	}
}

// The bootstrap always imports a small fixed set. A declared type import that
// names one of them must be skipped, not re-resolved: "net/http" has no dot in
// its first segment, so treating it as module-relative would emit an import of
// <module>/net/http and fail to compile.
func TestBootstrapSkipsFixedImportsInTypeImports(t *testing.T) {
	lock, graph := genFixtureLock(t)
	for i := range graph {
		if graph[i].Runtime.System == nil {
			continue
		}
		graph[i].Runtime.System.TypeImports = []string{"net/http", "context", "fmt", "errors"}
		graph[i].Runtime.System.Provides = []RuntimeProvide{
			{Field: "Handler", Capability: "http.handler", Type: "http.Handler"},
		}
	}
	for i := range lock.Modules {
		lock.Modules[i].Manifest = graph[i]
	}

	files, err := GenerateAll(context.Background(), "derivative.example/app", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	for _, file := range files {
		if file.Path != "internal/modules/bootstrap_registry_gen.go" {
			continue
		}
		for _, forbidden := range []string{
			"derivative.example/app/net/http",
			"derivative.example/app/context",
			"derivative.example/app/fmt",
			"derivative.example/app/errors",
		} {
			if strings.Contains(file.Content, forbidden) {
				t.Fatalf("bootstrap mis-resolved a stdlib import as %s:\n%s", forbidden, file.Content)
			}
		}
	}
}

// routeFixture builds a lock/graph whose single module contributes the supplied
// routes, so route-emitter tests state only what they are about.
func routeFixture(t *testing.T, routes ...RouteContribution) (Lock, []Manifest) {
	t.Helper()
	module := Manifest{
		ID: "ggg/page/sample", Kind: ModulePage, Name: "sample",
		Revision: 1, Contract: 1, Title: "Sample", Description: "Sample page.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Routes: routes},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/page/sample"},
		Modules: []LockedModule{{
			ID: "ggg/page/sample", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: module,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}
	return lock, []Manifest{module}
}

func routesRegistry(t *testing.T, lock Lock, graph []Manifest) string {
	t.Helper()
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	for _, file := range files {
		if file.Path == "internal/web/routes_registry_gen.go" {
			// gofmt aligns struct fields, so assertions collapse runs of spaces.
			// Pinning the formatter's padding would test gofmt, not the contract.
			return strings.Join(strings.Fields(file.Content), " ")
		}
	}
	t.Fatal("routes registry was not emitted")
	return ""
}

// The generated table is what the server registers, so it must carry the
// concrete pattern, the scope that selects the guards, the declared policy, and
// a handler resolver — not just a list of paths.
func TestRoutesRegistryEmitsConcreteRecords(t *testing.T) {
	lock, graph := routeFixture(t, RouteContribution{
		ID: "sample.show", Method: "GET", Pattern: "/sample", Scope: RoutePublic,
		Package: "internal/web", Handler: "handleSample",
	}, RouteContribution{
		ID: "sample.hook", Method: "POST", Pattern: "/hooks/sample", Scope: RouteWebhook,
		Package: "internal/web", Handler: "handleSampleHook",
		Policy: RoutePolicy{
			CSRFExempt: true, CSRFReason: "signed provider payload, not a browser form",
			MaxBodyBytes: 65536,
		},
	})
	registry := routesRegistry(t, lock, graph)

	for _, want := range []string{
		`ID: "sample.show"`,
		`Method: "GET"`,
		`Pattern: "/sample"`,
		`Scope: ScopePublic`,
		`Handler: func(s *Server) http.Handler { return http.HandlerFunc(s.handleSample) }`,
		`Scope: ScopeWebhook`,
		`CSRFExempt: true`,
		`CSRFReason: "signed provider payload, not a browser form"`,
		`MaxBodyBytes: 65536`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("routes registry missing %s:\n%s", want, registry)
		}
	}
}

// A conditional route declares its predicate. Registering it and answering 404
// would leak the path's existence; not registering it at all is the contract.
func TestRoutesRegistryEmitsEnabledPredicate(t *testing.T) {
	lock, graph := routeFixture(t, RouteContribution{
		ID: "sample.dev", Method: "GET", Pattern: "/dev/sample", Scope: RouteDev,
		Package: "internal/web", Handler: "handleDevSample", Enabled: "devBypassEnabled",
	})
	registry := routesRegistry(t, lock, graph)
	if !strings.Contains(registry, `Enabled: func(s *Server) bool { return s.devBypassEnabled() }`) {
		t.Fatalf("routes registry does not emit the predicate:\n%s", registry)
	}
}

// Patterns are validated against a real ServeMux at generation time. Two routes
// that conflict would otherwise panic on the first request in production, long
// after the generated file was committed.
func TestRoutesRegistryRejectsConflictingPatterns(t *testing.T) {
	cases := map[string][]RouteContribution{
		"exact duplicate": {
			{ID: "a", Method: "GET", Pattern: "/dup", Scope: RoutePublic, Package: "internal/web", Handler: "handleA"},
			{ID: "b", Method: "GET", Pattern: "/dup", Scope: RoutePublic, Package: "internal/web", Handler: "handleB"},
		},
		"wildcard equivalent": {
			{ID: "a", Method: "GET", Pattern: "/thing/{id}", Scope: RoutePublic, Package: "internal/web", Handler: "handleA"},
			{ID: "b", Method: "GET", Pattern: "/thing/{other}", Scope: RoutePublic, Package: "internal/web", Handler: "handleB"},
		},
		"malformed pattern": {
			{ID: "a", Method: "GET", Pattern: "/bad/{unclosed", Scope: RoutePublic, Package: "internal/web", Handler: "handleA"},
		},
	}
	for name, routes := range cases {
		t.Run(name, func(t *testing.T) {
			lock, graph := routeFixture(t, routes...)
			if _, err := GenerateAll(context.Background(), "example.com/acme", lock, graph); err == nil {
				t.Fatalf("GenerateAll accepted %s patterns", name)
			}
		})
	}
}

// An exemption without a stated reason is how a security hole gets reviewed and
// approved: the diff shows a bool, not a decision.
func TestRoutesRegistryRejectsUnexplainedExemption(t *testing.T) {
	lock, graph := routeFixture(t, RouteContribution{
		ID: "sample.hook", Method: "POST", Pattern: "/hooks/sample", Scope: RouteWebhook,
		Package: "internal/web", Handler: "handleSampleHook",
		Policy: RoutePolicy{CSRFExempt: true},
	})
	_, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err == nil {
		t.Fatal("GenerateAll accepted a CSRF exemption with no reason")
	}
	if !strings.Contains(err.Error(), "csrf") {
		t.Fatalf("error %v does not name the exemption", err)
	}
}

// Content types generate concrete routes. Until they do, the router has to walk
// a registry at boot, which means the generator cannot know every pattern and
// reserved prefix ahead of time — the whole point of the table.
func TestRoutesRegistryGeneratesContentTypeRoutes(t *testing.T) {
	module := Manifest{
		ID: "ggg/page/blog", Kind: ModulePage, Name: "blog",
		Revision: 1, Contract: 1, Title: "Blog", Description: "Blog pages.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{ContentTypes: []ContentTypeContribution{
			{ID: "post", Mode: ContentModePages, Paths: []string{"/blog"},
				Package: "internal/web", Handler: "handleContent"},
			{ID: "release", Mode: ContentModeSingle, Paths: []string{"/changelog"},
				Package: "internal/web", Handler: "handleContent"},
		}},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/page/blog"},
		Modules: []LockedModule{{
			ID: "ggg/page/blog", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: module,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}
	registry := routesRegistry(t, lock, []Manifest{module})

	// ModePages yields an index and a detail route; the detail handler must
	// receive the content type id so it can resolve the type without a registry
	// walk at request time.
	for _, want := range []string{
		`ID: "content.post.index", Method: "GET", Pattern: "/blog"`,
		`ID: "content.post.detail", Method: "GET", Pattern: "/blog/{slug}"`,
		`s.handleContentIndex("post")`,
		`s.handleContentDetail("post")`,
		// ModeSingle has no per-entry page, so no {slug} route exists at all.
		`ID: "content.release.index", Method: "GET", Pattern: "/changelog"`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("routes registry missing %s:\n%s", want, registry)
		}
	}
	if strings.Contains(registry, `/changelog/{slug}`) {
		t.Fatalf("single-page content type produced a detail route:\n%s", registry)
	}
}

// A content type whose public path collides with a route is a shadowing bug that
// Go's mux resolves silently in favour of the literal pattern, so the page just
// stops working. It must be a generation failure.
func TestRoutesRegistryRejectsContentPathCollidingWithRoute(t *testing.T) {
	page := Manifest{
		ID: "ggg/page/pricing", Kind: ModulePage, Name: "pricing",
		Revision: 1, Contract: 1, Title: "Pricing", Description: "Pricing page.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Routes: []RouteContribution{
			{ID: "pricing.show", Method: "GET", Pattern: "/pricing", Scope: RoutePublic,
				Package: "internal/web", Handler: "handlePricing"},
		}},
	}
	blog := Manifest{
		ID: "ggg/page/blog", Kind: ModulePage, Name: "blog",
		Revision: 1, Contract: 1, Title: "Blog", Description: "Blog pages.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{ContentTypes: []ContentTypeContribution{
			{ID: "post", Mode: ContentModeSingle, Paths: []string{"/pricing"},
				Package: "internal/web", Handler: "handleContent"},
		}},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/page/blog", "ggg/page/pricing"},
		Modules: []LockedModule{
			{ID: "ggg/page/blog", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: blog, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/page/pricing", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: page, Files: []LockedFile{}, Migrations: []LockedMigration{}},
		},
	}
	if _, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{blog, page}); err == nil {
		t.Fatal("GenerateAll accepted a content path that collides with a route pattern")
	}
}

// The smoke surface is generated so that adding a route changes what the smoke
// run visits. A hand-kept list of paths is the drift this closes: it keeps
// passing while covering less than it claims. Only a route a client can request
// by a fixed URL with no session qualifies — an instance id and a runtime
// predicate are not registry data.
func TestSmokeCasesCoverRequestablePublicGetRoutes(t *testing.T) {
	lock, graph := routeFixture(t,
		RouteContribution{
			ID: "sample.show", Method: "GET", Pattern: "/sample", Scope: RoutePublic,
			Package: "internal/web", Handler: "handleSample",
		},
		RouteContribution{
			ID: "sample.home", Method: "GET", Pattern: "/{$}", Scope: RoutePublic,
			Package: "internal/web", Handler: "handleSampleHome",
		},
		RouteContribution{
			ID: "sample.detail", Method: "GET", Pattern: "/sample/{slug}", Scope: RoutePublic,
			Package: "internal/web", Handler: "handleSampleDetail",
		},
		RouteContribution{
			ID: "sample.create", Method: "POST", Pattern: "/sample", Scope: RoutePublic,
			Package: "internal/web", Handler: "handleSampleCreate",
		},
		RouteContribution{
			ID: "sample.app", Method: "GET", Pattern: "/app/sample", Scope: RouteApp,
			Package: "internal/web", Handler: "handleSampleApp",
		},
		RouteContribution{
			ID: "sample.dev", Method: "GET", Pattern: "/dev/sample", Scope: RoutePublic,
			Package: "internal/web", Handler: "handleSampleDev", Enabled: "devBypassEnabled",
		},
	)

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, graph)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var content string
	for _, file := range files {
		if file.Path == "scripts/smoke_cases_registry_gen.txt" {
			content = file.Content
		}
	}
	if content == "" {
		t.Fatal("smoke case list was not emitted")
	}

	cases := make([]string, 0, 2)
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		cases = append(cases, line)
	}
	// Sorted by path, and "/{$}" is the root page rather than a parameter.
	want := []string{"sample.home /", "sample.show /sample"}
	if len(cases) != len(want) {
		t.Fatalf("smoke cases = %q, want %q", cases, want)
	}
	for i := range want {
		if cases[i] != want[i] {
			t.Fatalf("smoke cases = %q, want %q", cases, want)
		}
	}
}

// The dispatch table is generated from job declarations so an uninstalled
// module's kind is simply absent, which is what turns a stale queued row into an
// immediate dead-letter instead of a retried handler that cannot exist.
func TestJobsRegistryEmitsDispatchTableAndSchedulables(t *testing.T) {
	module := Manifest{
		ID: "ggg/workflow/digest", Kind: ModuleWorkflow, Name: "digest",
		Revision: 1, Contract: 1, Title: "Digest", Description: "Digest workflow.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Jobs: []JobContribution{
			{Kind: "email.digest", Package: "internal/jobs", Handler: "defineEmailDigest",
				Schedulable: true, MaxAttempts: 0},
			{Kind: "webhook.deliver", Package: "internal/jobs", Handler: "defineWebhookDeliver",
				Schedulable: false, MaxAttempts: 5},
		}},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/workflow/digest"},
		Modules: []LockedModule{{
			ID: "ggg/workflow/digest", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: module,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{module})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/jobs/jobs_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("jobs registry was not emitted")
	}

	for _, want := range []string{
		// The table names the declaration constructors; it never knows a payload type.
		"func workerDefinitions(w *Worker) []Definition",
		"w.defineEmailDigest()",
		"w.defineWebhookDeliver()",
		// The schedulable catalog is derived, not hand-listed.
		`var SchedulableKinds = []string{ "email.digest",`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("jobs registry missing %s:\n%s", want, registry)
		}
	}
	// webhook.deliver is not schedulable, so it must not appear in the catalog.
	catalog := registry[strings.Index(registry, "SchedulableKinds"):]
	if strings.Contains(catalog[:strings.Index(catalog, "}")], "webhook.deliver") {
		t.Fatalf("non-schedulable kind leaked into the catalog:\n%s", catalog)
	}
}

// Two modules declaring the same job kind is a dispatch ambiguity: whichever
// wins silently owns the other's queued rows.
func TestJobsRegistryRejectsDuplicateKinds(t *testing.T) {
	mk := func(id, name string) Manifest {
		return Manifest{
			ID: id, Kind: ModuleWorkflow, Name: name,
			Revision: 1, Contract: 1, Title: name, Description: name + " workflow.",
			Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
			Runtime: RuntimeContributions{Jobs: []JobContribution{
				{Kind: "same.kind", Package: "internal/jobs", Handler: "defineSame"},
			}},
		}
	}
	a, b := mk("ggg/workflow/a", "a"), mk("ggg/workflow/b", "b")
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/workflow/a", "ggg/workflow/b"},
		Modules: []LockedModule{
			{ID: "ggg/workflow/a", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: a, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/workflow/b", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: b, Files: []LockedFile{}, Migrations: []LockedMigration{}},
		},
	}
	if _, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{a, b}); err == nil {
		t.Fatal("GenerateAll accepted two modules declaring the same job kind")
	}
}

// Janitor sweeps are declared per module because each one deletes from a table
// its module owns. Uninstalling the module must take its sweep with it, or the
// generated pass calls a query that no longer exists.
func TestJobsRegistryEmitsDeclaredJanitors(t *testing.T) {
	audit := Manifest{
		ID: "ggg/system/audit", Kind: ModuleSystem, Name: "audit",
		Revision: 1, Contract: 1, Title: "Audit", Description: "Audit log.",
		Files: []ManifestFile{{Source: "internal/jobs/janitor_audit.go",
			Target: "internal/jobs/janitor_audit.go", Class: FileClassGo,
			SHA256: "0000000000000000000000000000000000000000000000000000000000000000"}},
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Janitors: []JanitorContribution{
			{Name: "audit_log", Package: "internal/jobs", Handler: "janitorAuditLog"},
		}},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/system/audit"},
		Modules: []LockedModule{{
			ID: "ggg/system/audit", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: audit,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{audit})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/jobs/jobs_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	for _, want := range []string{
		"func workerJanitors(w *Worker) []Janitor",
		`{Name: "audit_log", Sweep: w.janitorAuditLog},`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("janitor table missing %s:\n%s", want, registry)
		}
	}
}

// Two modules claiming one janitor name would make the operator-facing log
// ambiguous about which sweep failed.
func TestJobsRegistryRejectsDuplicateJanitorNames(t *testing.T) {
	mk := func(id, name string) Manifest {
		return Manifest{
			ID: id, Kind: ModuleSystem, Name: name,
			Revision: 1, Contract: 1, Title: name, Description: name + " system.",
			Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
			Runtime: RuntimeContributions{Janitors: []JanitorContribution{
				{Name: "same", Package: "internal/jobs", Handler: "janitorSame"},
			}},
		}
	}
	a, b := mk("ggg/system/a", "a"), mk("ggg/system/b", "b")
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/system/a", "ggg/system/b"},
		Modules: []LockedModule{
			{ID: "ggg/system/a", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: a, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/system/b", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: b, Files: []LockedFile{}, Migrations: []LockedMigration{}},
		},
	}
	if _, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{a, b}); err == nil {
		t.Fatal("GenerateAll accepted two modules declaring the same janitor name")
	}
}

func intp(n int) *int { return &n }

// The config parse is generated from declarations so a module's key and its Go
// field have one owner. This asserts the shape of each parse the generator
// supports, and that bounds and enums become refusals rather than silent
// fallbacks to the default.
func TestConfigRegistryEmitsTypedParse(t *testing.T) {
	module := Manifest{
		ID: "ggg/system/example", Kind: ModuleSystem, Name: "example",
		Revision: 1, Contract: 1, Title: "Example", Description: "Example system.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Environment: []EnvironmentVariable{
			{Key: "APP_ENV", Field: "Env", Type: EnvString, Description: "environment",
				Default: "development", Enum: []string{"development", "test", "production"}},
			{Key: "APP_URL", Field: "AppURL", Type: EnvString, Description: "base URL",
				Default: "http://localhost:8080", TrimSlash: true},
			{Key: "PORT", Field: "Port", Type: EnvInt, Description: "listen port",
				Default: "8080", Min: intp(1), Max: intp(65535)},
			{Key: "RETAIN_DAYS", Field: "RetainDays", Type: EnvInt, Description: "retention",
				Min: intp(0)},
			{Key: "MAINTENANCE_MODE", Field: "MaintenanceMode", Type: EnvBool, Description: "shed traffic"},
			{Key: "TEST_NOW", Field: "testNow", Type: EnvTime, Description: "frozen clock"},
			{Key: "SECRET_KEY", Field: "SecretKey", Type: EnvString, Description: "a secret",
				Secret: true, ProductionRequired: true},
		},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/system/example"},
		Modules: []LockedModule{{
			ID: "ggg/system/example", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: module,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{module})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/config/config_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("config registry was not emitted")
	}

	for _, want := range []string{
		// The struct carries the declared fields, including an unexported one.
		"Env string", "Port int", "MaintenanceMode bool", "testNow time.Time",
		// Errors accumulate; nothing returns early.
		"func parseDeclared(lookup func(string) string) (Config, []error)",
		// A bounded int names its bounds rather than failing vaguely.
		`must be an integer between 1 and 65535`,
		// A one-sided bound says only what it means.
		`must be an integer >= 0`,
		// A closed string names the accepted set.
		`must be one of development, test, production`,
		// Typed/default adapter access uses the same normalized values as fields.
		`cfg.Values["PORT"] = strconv.Itoa(cfg.Port)`,
		`cfg.Values["APP_URL"] = cfg.AppURL`,
		// Trailing slashes are stripped where declared, around the resolver
		// that applies the derived layer and records provenance.
		`strings.TrimRight(cfg.resolve(lookup, environment, "APP_URL", "http://localhost:8080"), "/")`,
		// The environment is resolved once, up front, so the derived layer
		// cannot depend on where APP_ENV lands in declaration order.
		`environment := pick(lookup, "APP_ENV", "development")`,
		// The derived table is always emitted, even when it is empty.
		`var derivedValues = map[string]map[string]string{`,
		// Production requirements are declared data, not an authored list.
		`SECRET_KEY is required when APP_ENV=production`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("config registry missing %q:\n%s", want, registry)
		}
	}
}

// One key declared by two modules means two owners for one Go field.
func TestConfigRegistryRejectsDuplicateKeys(t *testing.T) {
	mk := func(id, name string) Manifest {
		return Manifest{
			ID: id, Kind: ModuleSystem, Name: name,
			Revision: 1, Contract: 1, Title: name, Description: name + " system.",
			Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
			Environment: []EnvironmentVariable{
				{Key: "SHARED", Field: "Shared", Type: EnvString, Description: "shared"},
			},
		}
	}
	a, b := mk("ggg/system/a", "a"), mk("ggg/system/b", "b")
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/system/a", "ggg/system/b"},
		Modules: []LockedModule{
			{ID: "ggg/system/a", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: a, Files: []LockedFile{}, Migrations: []LockedMigration{}},
			{ID: "ggg/system/b", Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
				RequiredBy: []string{}, Manifest: b, Files: []LockedFile{}, Migrations: []LockedMigration{}},
		},
	}
	if _, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{a, b}); err == nil {
		t.Fatal("GenerateAll accepted one env key declared by two modules")
	}
}

// .env.example and the configuration reference are rendered from the same
// declarations as the parse, so neither can omit a key the code reads. The five
// keys this repo shipped without were exactly that kind of drift.
func TestEnvExampleAndReferenceCoverEveryDeclaredKey(t *testing.T) {
	module := Manifest{
		ID: "ggg/system/example", Kind: ModuleSystem, Name: "example",
		Revision: 1, Contract: 1, Title: "Example", Description: "Example system.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Environment: []EnvironmentVariable{
			{Key: "APP_ENV", Field: "Env", Type: EnvString, Description: "the environment",
				Default: "development", Enum: []string{"development", "production"}},
			{Key: "DATABASE_URL", Field: "DatabaseURL", Type: EnvString, Description: "connection string",
				Default: "postgres://localhost", ProductionRequired: true, Secret: true},
			{Key: "RATE_LIMIT_RPM", Field: "RateLimit", Type: EnvInt, Description: "per-IP budget",
				Default: "100", Min: intp(1)},
			{Key: "MAINTENANCE_MODE", Field: "MaintenanceMode", Type: EnvBool, Description: "shed traffic"},
		},
	}
	lock := Lock{
		Schema: 2, RegistryCommit: testCommitA, Order: []string{"ggg/system/example"},
		Modules: []LockedModule{{
			ID: "ggg/system/example", Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: module,
			Files: []LockedFile{}, Migrations: []LockedMigration{},
		}},
	}

	files, err := GenerateAll(context.Background(), "example.com/acme", lock, []Manifest{module})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	emitted := map[string]string{}
	for _, file := range files {
		emitted[file.Path] = file.Content
	}

	example, ok := emitted[".env.example"]
	if !ok {
		t.Fatal(".env.example was not emitted")
	}
	reference, ok := emitted["content/docs/configuration-reference.md"]
	if !ok {
		t.Fatal("configuration reference was not emitted")
	}

	for _, key := range []string{"APP_ENV", "DATABASE_URL", "RATE_LIMIT_RPM", "MAINTENANCE_MODE"} {
		if !strings.Contains(example, key+"=") {
			t.Fatalf(".env.example omits %s:\n%s", key, example)
		}
		if !strings.Contains(reference, "`"+key+"`") {
			t.Fatalf("reference omits %s:\n%s", key, reference)
		}
	}

	// A secret ships as an empty assignment: a plausible-looking fake value is
	// the kind of thing that reaches production.
	if !strings.Contains(example, "DATABASE_URL=\n") && !strings.HasSuffix(strings.TrimRight(example, "\n"), "DATABASE_URL=") {
		t.Fatalf("secret DATABASE_URL must ship with no value:\n%s", example)
	}
	// A non-secret default is filled in, so a fresh clone runs.
	if !strings.Contains(example, "APP_ENV=development") {
		t.Fatalf(".env.example must carry non-secret defaults:\n%s", example)
	}
	// The reference states what production refuses to boot without, and names
	// the registry the declaring module came from — this fixture's lock
	// records no namespace, so the source cell is a stated absence.
	if !strings.Contains(reference, "| `DATABASE_URL` | `ggg/system/example` | \u2014 | **production** |") {
		t.Fatalf("reference must mark production-required keys:\n%s", reference)
	}
	// Bounds reach the reader rather than living only in the error message.
	if !strings.Contains(reference, ">= 1") {
		t.Fatalf("reference must state declared bounds:\n%s", reference)
	}
}

const testDigest = "0000000000000000000000000000000000000000000000000000000000000000"

func localeModule(id, name string, locales map[string]map[string]string) Manifest {
	return Manifest{
		ID: id, Kind: ModulePage, Name: name,
		Revision: 1, Contract: 1, Title: name, Description: name + " page.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Locales: locales,
	}
}

func localeLockOf(modules []Manifest) Lock { return localeLock(modules...) }

func localeLock(modules ...Manifest) Lock {
	lock := Lock{Schema: 2, RegistryCommit: testCommitA}
	for _, m := range modules {
		lock.Order = append(lock.Order, m.ID)
		lock.Modules = append(lock.Modules, LockedModule{
			ID: m.ID, Revision: 1, Contract: 1, SourceCommit: testCommitA, Reason: "explicit",
			RequiredBy: []string{}, Manifest: m, Files: []LockedFile{}, Migrations: []LockedMigration{},
		})
	}
	return lock
}

// Catalogs are generated per locale from module-owned locale records, so a key
// belongs to exactly one module and every locale carries every key.
func TestI18nRegistryEmitsPerLocaleCatalogs(t *testing.T) {
	a := localeModule("ggg/page/home", "home", map[string]map[string]string{
		"en": {"home.title": "Ship your SaaS", "home.count": "%d projects"},
		"es": {"home.title": "Lanza tu SaaS", "home.count": "%d proyectos"},
	})
	b := localeModule("ggg/page/pricing", "pricing", map[string]map[string]string{
		"en": {"pricing.title": "Pricing"},
		"es": {"pricing.title": "Precios"},
	})

	files, err := GenerateAll(context.Background(), "example.com/acme", localeLock(a, b), []Manifest{a, b})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	emitted := map[string]string{}
	for _, file := range files {
		emitted[file.Path] = strings.Join(strings.Fields(file.Content), " ")
	}

	en, ok := emitted["internal/i18n/catalog_en_registry_gen.go"]
	if !ok {
		t.Fatal("en catalog was not emitted")
	}
	es, ok := emitted["internal/i18n/catalog_es_registry_gen.go"]
	if !ok {
		t.Fatal("es catalog was not emitted")
	}

	// Keys are sorted so the file is diffable, and both catalogs carry both modules.
	for _, want := range []string{
		`message.SetString(language.English, "home.count", "%d projects")`,
		`message.SetString(language.English, "home.title", "Ship your SaaS")`,
		`message.SetString(language.English, "pricing.title", "Pricing")`,
	} {
		if !strings.Contains(en, want) {
			t.Fatalf("en catalog missing %s:\n%s", want, en)
		}
	}
	if !strings.Contains(es, `message.SetString(language.Spanish, "home.title", "Lanza tu SaaS")`) {
		t.Fatalf("es catalog missing the Spanish value:\n%s", es)
	}
	if strings.Index(en, "home.count") > strings.Index(en, "home.title") {
		t.Fatalf("catalog keys are not sorted:\n%s", en)
	}

	// The locale list is derived, so the switcher cannot offer a locale no module
	// has translated.
	locales := emitted["internal/i18n/locales_registry_gen.go"]
	if !strings.Contains(locales, `{Code: "en", Tag: language.English`) ||
		!strings.Contains(locales, `{Code: "es", Tag: language.Spanish`) {
		t.Fatalf("locale registry is not derived from declarations:\n%s", locales)
	}
}

// One key owned by two modules means an unpredictable winner and a string that
// changes when an unrelated module is installed.
func TestI18nRegistryRejectsDuplicateKeyOwnership(t *testing.T) {
	a := localeModule("ggg/page/home", "home", map[string]map[string]string{
		"en": {"shared.title": "One"}, "es": {"shared.title": "Uno"},
	})
	b := localeModule("ggg/page/pricing", "pricing", map[string]map[string]string{
		"en": {"shared.title": "Two"}, "es": {"shared.title": "Dos"},
	})
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLock(a, b), []Manifest{a, b}); err == nil {
		t.Fatal("GenerateAll accepted one key owned by two modules")
	}
}

// A key present in one locale but not another renders as the raw key for some
// users. Parity is checked at generation time instead.
func TestI18nRegistryRejectsMissingTranslation(t *testing.T) {
	a := localeModule("ggg/page/home", "home", map[string]map[string]string{
		"en": {"home.title": "Ship", "home.subtitle": "Fast"},
		"es": {"home.title": "Lanza"},
	})
	err := func() error {
		_, err := GenerateAll(context.Background(), "example.com/acme", localeLock(a), []Manifest{a})
		return err
	}()
	if err == nil {
		t.Fatal("GenerateAll accepted a key missing from a declared locale")
	}
	if !strings.Contains(err.Error(), "home.subtitle") {
		t.Fatalf("error must name the untranslated key, got: %v", err)
	}
}

// Mismatched format placeholders are a runtime formatting bug in one language
// only — the kind that ships because nobody reads the other locale.
func TestI18nRegistryRejectsPlaceholderMismatch(t *testing.T) {
	a := localeModule("ggg/page/home", "home", map[string]map[string]string{
		"en": {"home.count": "%d of %d projects"},
		"es": {"home.count": "%d proyectos"},
	})
	_, err := GenerateAll(context.Background(), "example.com/acme", localeLock(a), []Manifest{a})
	if err == nil {
		t.Fatal("GenerateAll accepted mismatched format placeholders")
	}
	if !strings.Contains(err.Error(), "home.count") {
		t.Fatalf("error must name the key, got: %v", err)
	}
}

// A different verb for the same argument formats wrongly in one locale.
func TestI18nRegistryRejectsPlaceholderVerbChange(t *testing.T) {
	a := localeModule("ggg/page/home", "home", map[string]map[string]string{
		"en": {"home.owner": "Owned by %s"},
		"es": {"home.owner": "Propiedad de %d"},
	})
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLock(a), []Manifest{a}); err == nil {
		t.Fatal("GenerateAll accepted a changed format verb")
	}
}

func navModule(id, name string, nav []NavigationContribution, routes []RouteContribution) Manifest {
	return Manifest{
		ID: id, Kind: ModulePage, Name: name,
		Revision: 1, Contract: 1, Title: name, Description: name + " page.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Navigation: nav, Routes: routes},
	}
}

// Navigation is generated from route-linked declarations, so a nav link cannot
// point at a route that does not exist and installing a page installs its own
// nav entry.
func TestChromeRegistryEmitsOrderedNavigation(t *testing.T) {
	projects := navModule("ggg/page/projects", "projects",
		[]NavigationContribution{{
			ID: "nav.app.projects", Area: NavAreaApp, RouteID: "projects.index",
			LabelKey: "sidebar.projects", Match: "/app/projects", After: []string{"nav.app.dashboard"},
		}},
		[]RouteContribution{{
			ID: "projects.index", Method: "GET", Pattern: "/app/projects", Scope: RouteApp,
			Handler: "handleProjects",
		}})
	dashboard := navModule("ggg/page/dashboard", "dashboard",
		[]NavigationContribution{{
			ID: "nav.app.dashboard", Area: NavAreaApp, RouteID: "dashboard.index",
			LabelKey: "sidebar.dashboard", Match: "/app",
		}},
		[]RouteContribution{{
			ID: "dashboard.index", Method: "GET", Pattern: "/app", Scope: RouteApp,
			Handler: "handleDashboard",
		}})
	home := navModule("ggg/page/home", "home",
		[]NavigationContribution{
			{ID: "nav.public.features", Area: NavAreaPublic, Href: "/#features", LabelKey: "nav.features"},
			{ID: "footer.product.features", Area: NavAreaFooter, Group: "footer.product",
				Href: "/#features", LabelKey: "footer.features"},
		}, nil)
	settings := navModule("ggg/page/settings-webhooks", "settings-webhooks",
		[]NavigationContribution{{
			ID: "nav.settings.webhooks", Area: NavAreaSettings, RouteID: "settings-webhooks.index",
			LabelKey: "settings.tab_webhooks", Flags: []string{"webhooks"},
		}},
		[]RouteContribution{{
			ID: "settings-webhooks.index", Method: "GET", Pattern: "/app/settings/webhooks",
			Scope: RouteApp, Package: "internal/web", Handler: "handleSettingsWebhooks",
		}})

	// Declared out of order on purpose: the emitted order must come from the
	// declared before/after edges, not from manifest sequence.
	mods := []Manifest{projects, dashboard, home, settings}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var chrome string
	for _, file := range files {
		if file.Path == "internal/web/templates/chrome_registry_gen.go" {
			chrome = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if chrome == "" {
		t.Fatal("chrome registry was not emitted")
	}

	// Hrefs come from the route table, not from a hand-typed string. Match is
	// omitted when it equals the href, because MatchPath already falls back to it.
	for _, want := range []string{
		`var AppNav = []NavItem{`,
		`{ID: "nav.app.dashboard", LabelKey: "sidebar.dashboard", Href: "/app"}`,
		`{ID: "nav.app.projects", LabelKey: "sidebar.projects", Href: "/app/projects"}`,
		`var PublicNav = []NavItem{`,
		`{ID: "nav.public.features", LabelKey: "nav.features", Href: "/#features"}`,
		`var FooterColumns = []NavColumn{`,
		`{TitleKey: "footer.product", Items: []NavItem{`,
	} {
		if !strings.Contains(chrome, want) {
			t.Fatalf("chrome registry missing %s:\n%s", want, chrome)
		}
	}
	if strings.Index(chrome, "nav.app.projects") < strings.Index(chrome, "nav.app.dashboard") {
		t.Fatalf("declared after-edge was not honoured:\n%s", chrome)
	}

	// A flag-gated entry keeps its condition, so the settings tab strip still
	// hides behind its feature flag.
	var settingsNav string
	for _, file := range files {
		if file.Path == "internal/web/templates/settings_navigation_registry_gen.go" {
			settingsNav = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if !strings.Contains(settingsNav, `Flags: []string{"webhooks"}`) {
		t.Fatalf("settings registry dropped the flag gate:\n%s", settingsNav)
	}
}

// A nav entry pointing at a route no module registers is a dead link, and it is
// exactly the drift that generation exists to prevent.
func TestChromeRegistryRejectsUnknownRoute(t *testing.T) {
	orphan := navModule("ggg/page/orphan", "orphan", []NavigationContribution{{
		ID: "nav.app.orphan", Area: NavAreaApp, RouteID: "nope.index", LabelKey: "sidebar.orphan",
	}}, nil)
	mods := []Manifest{orphan}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted a nav entry pointing at an unregistered route")
	}
}

// A cycle in before/after has no stable order; picking one silently would make
// the sidebar depend on map iteration.
func TestChromeRegistryRejectsOrderingCycle(t *testing.T) {
	a := navModule("ggg/page/a", "a", []NavigationContribution{{
		ID: "nav.app.a", Area: NavAreaApp, Href: "/app/a", LabelKey: "sidebar.a",
		After: []string{"nav.app.b"},
	}}, nil)
	b := navModule("ggg/page/b", "b", []NavigationContribution{{
		ID: "nav.app.b", Area: NavAreaApp, Href: "/app/b", LabelKey: "sidebar.b",
		After: []string{"nav.app.a"},
	}}, nil)
	mods := []Manifest{a, b}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted a navigation ordering cycle")
	}
}

func apiModule(id, name string, routes []RouteContribution, api *OpenAPIContribution) Manifest {
	return Manifest{
		ID: id, Kind: ModuleWorkflow, Name: name,
		Revision: 1, Contract: 1, Title: name, Description: name + " workflow.",
		Files: []ManifestFile{}, Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Runtime: RuntimeContributions{Routes: routes}, OpenAPI: api,
	}
}

// The spec is generated from route declarations, so a documented operation and
// a served endpoint are the same list. Security and the idempotency parameter
// are derived from the route's declared scope and policy rather than restated,
// because a document that disagrees with middleware is worse than no document.
func TestOpenAPIRegistryDerivesSecurityAndIdempotency(t *testing.T) {
	projects := apiModule("ggg/workflow/api-projects", "api-projects",
		[]RouteContribution{
			{ID: "api.projects.list", Method: "GET", Pattern: "/api/v1/projects",
				Scope: RouteAPIRead, Package: "internal/web", Handler: "handleAPIProjectsList"},
			{ID: "api.projects.create", Method: "POST", Pattern: "/api/v1/projects",
				Scope: RouteAPIWrite, Package: "internal/web", Handler: "handleAPIProjectsCreate",
				Policy: RoutePolicy{Idempotent: true}},
		},
		&OpenAPIContribution{
			Info:    json.RawMessage(`{"title":"Acme API","version":"1.0.0"}`),
			Servers: json.RawMessage(`[{"url":"/"}]`),
			Tags:    []OpenAPITag{{Name: "projects", Description: "The CRUD resource."}},
			Components: map[string]map[string]json.RawMessage{
				"schemas": {"Project": json.RawMessage(`{"type":"object"}`)},
			},
			Operations: []OpenAPIOperation{
				{RouteID: "api.projects.list", OperationID: "listProjects", Summary: "List projects.",
					Tags:      []string{"projects"},
					Responses: json.RawMessage(`{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Project"}}}}}`)},
				{RouteID: "api.projects.create", OperationID: "createProject", Summary: "Create a project.",
					Tags:        []string{"projects"},
					RequestBody: json.RawMessage(`{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Project"}}}}`),
					Responses:   json.RawMessage(`{"201":{"description":"created"}}`)},
			},
		})

	mods := []Manifest{projects}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var spec string
	for _, file := range files {
		if file.Path == "internal/api/openapi_registry_gen.yaml" {
			spec = file.Content
		}
	}
	if spec == "" {
		t.Fatal("spec was not emitted")
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(spec), &doc); err != nil {
		t.Fatalf("emitted spec is not valid YAML: %v\n%s", err, spec)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("spec version = %v", doc["openapi"])
	}

	paths, _ := doc["paths"].(map[string]any)
	projectsPath, ok := paths["/api/v1/projects"].(map[string]any)
	if !ok {
		t.Fatalf("path missing from spec:\n%s", spec)
	}

	// A read route requires the read scope; a write route requires write.
	get, _ := projectsPath["get"].(map[string]any)
	if fmt.Sprint(get["security"]) != `[map[bearerAuth:[read]]]` {
		t.Fatalf("read security = %v", get["security"])
	}
	post, _ := projectsPath["post"].(map[string]any)
	if fmt.Sprint(post["security"]) != `[map[bearerAuth:[write]]]` {
		t.Fatalf("write security = %v", post["security"])
	}

	// The idempotency header is derived from the route policy, so a route that
	// accepts the key always documents it and one that does not never claims to.
	if !strings.Contains(fmt.Sprint(post["parameters"]), "#/components/parameters/IdempotencyKey") {
		t.Fatalf("idempotent route must reference the generated parameter: %v", post["parameters"])
	}
	if strings.Contains(fmt.Sprint(get["parameters"]), "IdempotencyKey") {
		t.Fatalf("non-idempotent route must not reference it: %v", get["parameters"])
	}
	// The referenced component is generated too, so the reference resolves.
	generatedParams, _ := doc["components"].(map[string]any)["parameters"].(map[string]any)
	if _, ok := generatedParams["IdempotencyKey"]; !ok {
		t.Fatalf("the generated parameter component is missing:\n%s", spec)
	}

	// The security scheme is emitted for the document, not hand-declared.
	components, _ := doc["components"].(map[string]any)
	if _, ok := components["securitySchemes"]; !ok {
		t.Fatalf("securitySchemes missing:\n%s", spec)
	}
}

// An API route with no operation ships an undocumented endpoint; that is the
// drift the parity test used to catch after the fact.
func TestOpenAPIRegistryRejectsUndocumentedRoute(t *testing.T) {
	mod := apiModule("ggg/workflow/api-x", "api-x",
		[]RouteContribution{{ID: "api.x.list", Method: "GET", Pattern: "/api/v1/x",
			Scope: RouteAPIRead, Package: "internal/web", Handler: "handleX"}},
		&OpenAPIContribution{Info: json.RawMessage(`{"title":"x","version":"1"}`)})
	mods := []Manifest{mod}
	_, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err == nil {
		t.Fatal("GenerateAll accepted an undocumented API route")
	}
	if !strings.Contains(err.Error(), "api.x.list") {
		t.Fatalf("error must name the route: %v", err)
	}
}

// An operation for a route nobody serves documents an endpoint that 404s.
func TestOpenAPIRegistryRejectsOrphanOperation(t *testing.T) {
	mod := apiModule("ggg/workflow/api-x", "api-x", nil, &OpenAPIContribution{
		Info: json.RawMessage(`{"title":"x","version":"1"}`),
		Operations: []OpenAPIOperation{{
			RouteID: "api.ghost", OperationID: "ghost", Summary: "Nope.",
			Responses: json.RawMessage(`{"200":{"description":"ok"}}`),
		}},
	})
	mods := []Manifest{mod}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted an operation for an unserved route")
	}
}

// A $ref that resolves to nothing makes the document unusable by a generator or
// validator, and it is invisible until someone runs one.
func TestOpenAPIRegistryRejectsDanglingRef(t *testing.T) {
	mod := apiModule("ggg/workflow/api-x", "api-x",
		[]RouteContribution{{ID: "api.x.list", Method: "GET", Pattern: "/api/v1/x",
			Scope: RouteAPIRead, Package: "internal/web", Handler: "handleX"}},
		&OpenAPIContribution{
			Info: json.RawMessage(`{"title":"x","version":"1"}`),
			Operations: []OpenAPIOperation{{
				RouteID: "api.x.list", OperationID: "listX", Summary: "List.",
				Responses: json.RawMessage(`{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Missing"}}}}}`),
			}},
		})
	mods := []Manifest{mod}
	_, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err == nil {
		t.Fatal("GenerateAll accepted a dangling $ref")
	}
	if !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("error must name the unresolved ref: %v", err)
	}
}

// Two modules claiming one operationId or one schema id makes the winner depend
// on install order.
func TestOpenAPIRegistryRejectsDuplicateIdentifiers(t *testing.T) {
	mk := func(id, routeID, pattern, opID, schemaID string) Manifest {
		return apiModule(id, strings.TrimPrefix(id, "ggg/workflow/"),
			[]RouteContribution{{ID: routeID, Method: "GET", Pattern: pattern,
				Scope: RouteAPIRead, Package: "internal/web", Handler: "handleX"}},
			&OpenAPIContribution{
				Components: map[string]map[string]json.RawMessage{
					"schemas": {schemaID: json.RawMessage(`{"type":"object"}`)},
				},
				Operations: []OpenAPIOperation{{
					RouteID: routeID, OperationID: opID, Summary: "List.",
					Responses: json.RawMessage(`{"200":{"description":"ok"}}`),
				}},
			})
	}
	info := json.RawMessage(`{"title":"x","version":"1"}`)

	dupOp := []Manifest{mk("ggg/workflow/a", "api.a", "/api/v1/a", "same", "A"),
		mk("ggg/workflow/b", "api.b", "/api/v1/b", "same", "B")}
	dupOp[0].OpenAPI.Info = info
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(dupOp), dupOp); err == nil {
		t.Fatal("GenerateAll accepted a duplicate operationId")
	}

	dupSchema := []Manifest{mk("ggg/workflow/a", "api.a", "/api/v1/a", "opA", "Same"),
		mk("ggg/workflow/b", "api.b", "/api/v1/b", "opB", "Same")}
	dupSchema[0].OpenAPI.Info = info
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(dupSchema), dupSchema); err == nil {
		t.Fatal("GenerateAll accepted a duplicate schema id")
	}
}

func requirementList(ids []string) []Requirement {
	out := make([]Requirement, 0, len(ids))
	for _, id := range ids {
		out = append(out, Requirement{ID: id, Contract: ContractBounds{Min: 1, Max: 1}})
	}
	return out
}

func queryModule(id, name string, tables []string, queries []QueryContribution, requires []string) Manifest {
	data := make([]DataDeclaration, 0, len(tables))
	for _, table := range tables {
		data = append(data, DataDeclaration{
			Table: table, Scope: DataScopeOrg, Export: false,
			AccountDelete: DeleteRetain, OrganizationDelete: DeleteRetain,
		})
	}
	if requires == nil {
		requires = []string{}
	}
	return Manifest{
		ID: id, Kind: ModuleSystem, Name: name,
		Revision: 1, Contract: 1, Title: name, Description: name + " system.",
		Files: []ManifestFile{}, Requires: requirementList(requires), RemovalPolicy: RemovalFree,
		Data: data, Runtime: RuntimeContributions{Queries: queries},
	}
}

// Query ownership is declared so a method name has one owner and a module
// reading another module's table has to say so. Both are invisible in a shared
// sqlc package otherwise.
func TestQueriesRegistryEmitsOwnership(t *testing.T) {
	projects := queryModule("ggg/system/projects", "projects", []string{"projects"},
		[]QueryContribution{
			{Name: "ListProjects", Table: "projects"},
			{Name: "CreateProject", Table: "projects"},
		}, nil)
	export := queryModule("ggg/workflow/project-export", "project-export", nil,
		[]QueryContribution{{Name: "ExportProjectRows", Table: "projects"}},
		[]string{"ggg/system/projects"})

	mods := []Manifest{projects, export}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/db/queries_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("query registry was not emitted")
	}
	for _, want := range []string{
		`"CreateProject": "ggg/system/projects"`,
		`"ListProjects": "ggg/system/projects"`,
		// A declared dependency is what makes reading another owner's table legal.
		`"ExportProjectRows": "ggg/workflow/project-export"`,
		`"projects": "ggg/system/projects"`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("query registry missing %s:\n%s", want, registry)
		}
	}
}

// One method name owned twice means the sqlc package has two definitions, or
// silently keeps one.
func TestQueriesRegistryRejectsDuplicateMethod(t *testing.T) {
	a := queryModule("ggg/system/a", "a", []string{"ta"}, []QueryContribution{{Name: "Same", Table: "ta"}}, nil)
	b := queryModule("ggg/system/b", "b", []string{"tb"}, []QueryContribution{{Name: "Same", Table: "tb"}}, nil)
	mods := []Manifest{a, b}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted one sqlc method name owned twice")
	}
}

// Reading another module's table without declaring the dependency means the
// query silently breaks when that module is removed.
func TestQueriesRegistryRejectsUndeclaredCrossOwnerAccess(t *testing.T) {
	owner := queryModule("ggg/system/owner", "owner", []string{"secrets"},
		[]QueryContribution{{Name: "GetSecret", Table: "secrets"}}, nil)
	peeker := queryModule("ggg/system/peeker", "peeker", nil,
		[]QueryContribution{{Name: "PeekSecret", Table: "secrets"}}, nil)
	mods := []Manifest{owner, peeker}
	_, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err == nil {
		t.Fatal("GenerateAll accepted cross-owner table access with no declared dependency")
	}
	if !strings.Contains(err.Error(), "ggg/system/owner") {
		t.Fatalf("error must name the owning module: %v", err)
	}
}

// A query against a table nobody declares has no owner to depend on, and no
// declared delete or export behaviour either.
func TestQueriesRegistryRejectsUnknownTable(t *testing.T) {
	mod := queryModule("ggg/system/a", "a", nil, []QueryContribution{{Name: "Q", Table: "ghost"}}, nil)
	mods := []Manifest{mod}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted a query against an undeclared table")
	}
}

// The embed set is derived from declarations, so every static asset a module
// ships is compiled into the binary because the Go compiler itself refuses an
// embed pattern that matches no file — coverage is not a test, it is the build.
func TestStaticRegistryEmitsEmbedAndOwnership(t *testing.T) {
	shell := Manifest{
		ID: "ggg/system/static", Kind: ModuleSystem, Name: "static",
		Revision: 1, Contract: 1, Title: "Static", Description: "Static shell assets.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Files: []ManifestFile{
			{Source: "static/app.js", Target: "static/app.js", Class: FileClassAsset, SHA256: testDigest, RewriteModule: false},
			{Source: "static/fonts/inter-var.woff2", Target: "static/fonts/inter-var.woff2", Class: FileClassAsset, SHA256: testDigest, RewriteModule: false},
		},
	}
	identity := Manifest{
		ID: "ggg/system/identity", Kind: ModuleSystem, Name: "identity",
		Revision: 1, Contract: 1, Title: "Identity", Description: "Identity.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Files: []ManifestFile{
			{Source: "static/vendor/clerk.js", Target: "static/vendor/clerk.js", Class: FileClassAsset, SHA256: testDigest, RewriteModule: false},
		},
	}
	// A Go file under static/ is source, not an asset: embed patterns must not
	// include it, or the compiler refuses the glob.
	shell.Files = append(shell.Files, ManifestFile{
		Source: "static/static.go", Target: "static/static.go", Class: FileClassGo, SHA256: testDigest, RewriteModule: true,
	})

	mods := []Manifest{shell, identity}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "static/embed_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("static registry was not emitted")
	}
	for _, want := range []string{
		"//go:embed app.js fonts/inter-var.woff2 vendor/clerk.js",
		"var FS embed.FS",
		`"static/app.js": "ggg/system/static"`,
		`"static/vendor/clerk.js": "ggg/system/identity"`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("static registry missing %q:\n%s", want, registry)
		}
	}
	// static.go itself is source and must appear in neither the embed nor the map.
	if strings.Contains(registry, "static.go") {
		t.Fatalf("Go source leaked into the embed set:\n%s", registry)
	}
}

// static.go is legitimately Go source under static/, and assets are class
// asset — but any other class there is a declaration bug that would produce a
// broken embed pattern, so it is refused rather than guessed at.
func TestStaticRegistryRejectsNonAssetNonGoClass(t *testing.T) {
	bad := Manifest{
		ID: "ggg/system/static", Kind: ModuleSystem, Name: "static",
		Revision: 1, Contract: 1, Title: "Static", Description: "x.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Files: []ManifestFile{
			{Source: "static/theme.css", Target: "static/theme.css", Class: FileClassStyle, SHA256: testDigest, RewriteModule: false},
		},
	}
	mods := []Manifest{bad}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted a style-classed file under static/")
	}
}

// The lifecycle registry is what turns a data declaration into an obligation
// the repository can check: deletion expects the declared FK behaviour, and an
// export declaration must correspond to a collection somewhere. Without it a
// newly installed data module is omitted silently — exactly what the item
// forbids.
func TestDataLifecycleRegistryEmitsDeclarations(t *testing.T) {
	mod := queryModule("ggg/system/widgets", "widgets", []string{"widgets"},
		nil, nil)
	mod.Data[0].Export = true
	mod.Data[0].AccountDelete = DeleteRetain
	mod.Data[0].OrganizationDelete = DeleteCascade
	other := queryModule("ggg/system/gadgets", "gadgets", []string{"gadgets"}, nil, nil)
	other.Data[0].Scope = DataScopeUser
	other.Data[0].Export = false
	other.Data[0].AccountDelete = DeleteManual

	mods := []Manifest{mod, other}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/db/data_lifecycle_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("data lifecycle registry was not emitted")
	}
	for _, want := range []string{
		`{Table: "gadgets", Module: "ggg/system/gadgets", Scope: "user", Export: false, AccountDelete: "manual", OrganizationDelete: "retain"}`,
		`{Table: "widgets", Module: "ggg/system/widgets", Scope: "org", Export: true, AccountDelete: "retain", OrganizationDelete: "cascade"}`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("lifecycle registry missing %s:\n%s", want, registry)
		}
	}
}

// Seed order is generated from declarations, so a module's fixture loads with
// the module and a removed module's fixture stops loading — instead of a
// monolithic SQL file nobody could attribute.
func TestSeedRegistryEmitsOrderedFragments(t *testing.T) {
	orgs := Manifest{
		ID: "ggg/system/organizations", Kind: ModuleSystem, Name: "organizations",
		Revision: 1, Contract: 1, Title: "Orgs", Description: "x.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Files: []ManifestFile{
			{Source: "internal/db/testdata/seed/dev/organizations.sql",
				Target: "internal/db/testdata/seed/dev/organizations.sql",
				Class:  FileClassSeed, SHA256: testDigest, RewriteModule: false},
			{Source: "internal/db/testdata/seed/e2e/organizations.sql",
				Target: "internal/db/testdata/seed/e2e/organizations.sql",
				Class:  FileClassSeed, SHA256: testDigest, RewriteModule: false},
		},
	}
	flags := Manifest{
		ID: "ggg/system/feature-flags", Kind: ModuleSystem, Name: "feature-flags",
		Revision: 1, Contract: 1, Title: "Flags", Description: "x.",
		Requires: []Requirement{{ID: "ggg/system/organizations", Contract: ContractBounds{Min: 1, Max: 1}}}, RemovalPolicy: RemovalFree,
		Files: []ManifestFile{
			{Source: "internal/db/testdata/seed/dev/flags.sql",
				Target: "internal/db/testdata/seed/dev/flags.sql",
				Class:  FileClassSeed, SHA256: testDigest, RewriteModule: false},
		},
	}

	// Declaration order is deliberately scrambled; the lock order reflects
	// the dependency edge, as a real lock does (sync writes a topological order).
	mods := []Manifest{flags, orgs}
	lock := localeLockOf([]Manifest{orgs, flags})
	files, err := GenerateAll(context.Background(), "example.com/acme", lock, mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var registry string
	for _, file := range files {
		if file.Path == "internal/db/seed_registry_gen.go" {
			registry = strings.Join(strings.Fields(file.Content), " ")
		}
	}
	if registry == "" {
		t.Fatal("seed registry was not emitted")
	}
	for _, want := range []string{
		"var SeedFragments = map[string][]string{",
		`"dev": {`,
		// Organizations precedes flags because the org rows must exist before
		// anything referencing them loads, and lock order says so regardless of
		// the order the fragments were declared in.
		strings.Join(strings.Fields(`"internal/db/testdata/seed/dev/organizations.sql", "internal/db/testdata/seed/dev/flags.sql"`), " "),
		`"internal/db/testdata/seed/e2e/organizations.sql"`,
	} {
		if !strings.Contains(registry, want) {
			t.Fatalf("seed registry missing %q:\n%s", want, registry)
		}
	}
}

// Personas are declared once, next to the identity records they exercise, and
// both the e2e helper and the fixture-parity check read the same declaration.
func TestPersonasRegistryEmitsTypeScript(t *testing.T) {
	identity := Manifest{
		ID: "ggg/system/identity", Kind: ModuleSystem, Name: "identity",
		Revision: 1, Contract: 1, Title: "Identity", Description: "x.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Personas: []PersonaContribution{
			{ID: "pro", User: "user_pro", Org: "org_pro", Role: "org:admin"},
			{ID: "noorg", User: "user_noorg", Org: "", Role: ""},
		},
	}
	mods := []Manifest{identity}
	files, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var ts string
	for _, file := range files {
		if file.Path == "e2e/generated/personas.ts" {
			ts = file.Content
		}
	}
	if ts == "" {
		t.Fatal("personas.ts was not emitted")
	}
	for _, want := range []string{
		"export type PersonaId = 'pro' | 'noorg';",
		"{ id: 'pro', user: 'user_pro', org: 'org_pro', role: 'org:admin' },",
		"export function sessionFor(p: PersonaId): string",
	} {
		if !strings.Contains(ts, want) {
			t.Fatalf("personas.ts missing %q:\n%s", want, ts)
		}
	}
}

// Two personas with one id means two specs silently share an actor.
func TestPersonasRegistryRejectsDuplicateIDs(t *testing.T) {
	a := Manifest{ID: "ggg/system/identity", Kind: ModuleSystem, Name: "identity",
		Revision: 1, Contract: 1, Title: "Identity", Description: "x.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Personas: []PersonaContribution{{ID: "pro", User: "u1", Org: "o1", Role: "org:admin"}}}
	b := Manifest{ID: "ggg/workflow/x", Kind: ModuleWorkflow, Name: "x",
		Revision: 1, Contract: 1, Title: "X", Description: "x.",
		Requires: []Requirement{}, RemovalPolicy: RemovalFree,
		Personas: []PersonaContribution{{ID: "pro", User: "u2", Org: "o2", Role: "org:admin"}}}
	mods := []Manifest{a, b}
	if _, err := GenerateAll(context.Background(), "example.com/acme", localeLockOf(mods), mods); err == nil {
		t.Fatal("GenerateAll accepted a duplicate persona id")
	}
}

// The UI metadata registry is what lets gallery coverage compare rendered
// output against installed modules rather than a hand-kept list. A component
// declared by a module must appear with its owning module and family, so an
// uninstalled module's components vanish from the reference automatically.
func TestUIComponentRegistryEmitsOwnedComponents(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/element/ui-core", Kind: ModuleElement, Name: "ui-core", Revision: 1, Contract: 1,
		Runtime: RuntimeContributions{UI: []UIContribution{
			{Name: "badge", Family: GalleryFeedback},
			{Name: "dialog", Family: GalleryOverlays, Engine: "alpine", Alpine: "uiDialog"},
		}},
	}}
	lock := Lock{Order: []string{"ggg/element/ui-core"}, Modules: []LockedModule{{ID: "ggg/element/ui-core"}}}
	f, err := emitUIComponentRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, want := range []string{
		`Name: "badge"`, `Family: "feedback"`, `Module: "ggg/element/ui-core"`,
		`Alpine: "uiDialog"`, `Engine: "alpine"`,
	} {
		if !strings.Contains(f.Content, want) {
			t.Fatalf("ui registry missing %s:\n%s", want, f.Content)
		}
	}
}

// Two modules must never claim the same component name: the gallery reference
// would show one entry whose ownership depends on iteration order, and removing
// either module would leave a live reference to a component nobody installs.
func TestUIComponentRegistryRejectsDuplicateComponent(t *testing.T) {
	mods := []Manifest{
		{ID: "ggg/element/ui-core", Kind: ModuleElement, Name: "ui-core", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{UI: []UIContribution{{Name: "badge", Family: GalleryFeedback}}}},
		{ID: "ggg/component/badge-two", Kind: ModuleComponent, Name: "badge-two", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{UI: []UIContribution{{Name: "badge", Family: GalleryFeedback}}}},
	}
	lock := Lock{Order: []string{"ggg/element/ui-core", "ggg/component/badge-two"},
		Modules: []LockedModule{{ID: "ggg/element/ui-core"}, {ID: "ggg/component/badge-two"}}}
	if _, err := emitUIComponentRegistry(context.Background(), "example.com/app", lock, mods); err == nil {
		t.Fatal("two modules declaring the same component must be rejected")
	} else if !strings.Contains(err.Error(), "badge") {
		t.Fatalf("error must name the contested component: %v", err)
	}
}

// The shell cannot hard-code module script paths: modules are installed and
// removed. The generated fragment list is what lets the shell load exactly the
// Alpine sources the selected modules ship, in lock order, before Alpine boots.
func TestAlpineFragmentRegistryListsOwnedScripts(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/element/ui-core", Kind: ModuleElement, Name: "ui-core", Revision: 1, Contract: 1,
		Files: []ManifestFile{
			{Source: "static/ui/overlays.js", Target: "static/ui/overlays.js", Class: FileClassAsset},
		},
		Runtime: RuntimeContributions{
			UI: []UIContribution{{Name: "dialog", Family: GalleryOverlays, Engine: "alpine", Alpine: "uiDialog"}},
			Assets: []AssetContribution{
				{ID: "ui-overlays", Path: "static/ui/overlays.js", Kind: AssetScript},
				{ID: "ui-sprite", Path: "static/ui/sprite.svg", Kind: AssetImage},
			},
		},
	}}
	lock := Lock{Order: []string{"ggg/element/ui-core"}, Modules: []LockedModule{{ID: "ggg/element/ui-core"}}}
	f, err := emitAlpineFragments(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(f.Content, `"/static/ui/overlays.js"`) {
		t.Fatalf("fragment list missing the owned script:\n%s", f.Content)
	}
	if strings.Contains(f.Content, "sprite.svg") {
		t.Fatal("only script assets are Alpine fragments")
	}
}

// A module that ships no Alpine fragment must contribute nothing: an empty
// script tag is a wasted request, and a path to a file no module installs is a
// 404 in every deployment.
func TestAlpineFragmentRegistryOmitsModulesWithoutScripts(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/system/audit", Kind: ModuleSystem, Name: "audit", Revision: 1, Contract: 1,
		Files: []ManifestFile{{Source: "internal/audit/audit.go", Target: "internal/audit/audit.go", Class: FileClassGo}},
	}}
	lock := Lock{Order: []string{"ggg/system/audit"}, Modules: []LockedModule{{ID: "ggg/system/audit"}}}
	f, err := emitAlpineFragments(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if strings.Contains(f.Content, "/static/") {
		t.Fatalf("module ships no fragment but a path was emitted:\n%s", f.Content)
	}
}

// An advanced widget's vendor asset must not load with the shell: a project
// that never renders a chart should never pay for Chart.js. The generated engine
// registry is what lets the shell's loader fetch a versioned asset on demand,
// and the integrity hash is what stops a swapped file from executing.
func TestEngineRegistryEmitsVersionedAssetsWithIntegrity(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/component/chart", Kind: ModuleComponent, Name: "chart", Revision: 1, Contract: 1,
		Runtime: RuntimeContributions{Assets: []AssetContribution{
			{
				ID: "chartjs", Path: "static/vendor/chartjs-4.5.1.umd.min.js", Kind: AssetScript,
				Engine:    "chartjs",
				Integrity: "sha256-SERKgtTty1vsDxll+qzd4Y2cF9swY9BCq62i9wXJ9Uo=",
			},
			// A shell script has no engine: it loads with the page.
			{ID: "ui-overlays", Path: "static/ui/overlays.js", Kind: AssetScript},
		}},
	}}
	lock := Lock{Order: []string{"ggg/component/chart"}, Modules: []LockedModule{{ID: "ggg/component/chart"}}}

	f, err := emitEngineRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	for _, want := range []string{
		`"chartjs"`,
		`/static/vendor/chartjs-4.5.1.umd.min.js`,
		"sha256-SERKgtTty1vsDxll+qzd4Y2cF9swY9BCq62i9wXJ9Uo=",
	} {
		if !strings.Contains(f.Content, want) {
			t.Fatalf("engine registry missing %s:\n%s", want, f.Content)
		}
	}
	if strings.Contains(f.Content, "overlays.js") {
		t.Fatal("a shell fragment is not a lazily loaded engine")
	}
}

// Two modules must not claim one engine name: the loader would fetch whichever
// asset won the iteration, and removing either module would break the other.
func TestEngineRegistryRejectsDuplicateEngine(t *testing.T) {
	asset := func(path string) []AssetContribution {
		return []AssetContribution{{
			ID: "chartjs", Path: path, Kind: AssetScript,
			Engine: "chartjs", Integrity: "sha256-AAAA",
		}}
	}
	mods := []Manifest{
		{ID: "ggg/component/chart", Kind: ModuleComponent, Name: "chart", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{Assets: asset("static/vendor/a.js")}},
		{ID: "ggg/component/graph", Kind: ModuleComponent, Name: "graph", Revision: 1, Contract: 1,
			Runtime: RuntimeContributions{Assets: asset("static/vendor/b.js")}},
	}
	lock := Lock{
		Order:   []string{"ggg/component/chart", "ggg/component/graph"},
		Modules: []LockedModule{{ID: "ggg/component/chart"}, {ID: "ggg/component/graph"}},
	}
	if _, err := emitEngineRegistry(context.Background(), "example.com/app", lock, mods); err == nil {
		t.Fatal("two modules claiming one engine must be rejected")
	} else if !strings.Contains(err.Error(), "chartjs") {
		t.Fatalf("error must name the contested engine: %v", err)
	}
}

// An engine asset is loaded on demand by definition, so it must never appear in
// the shell fragment list. Both are kind "script", and the fragment emitter
// originally filtered on kind alone - which put Chart.js in the head of every
// page and made the whole lazy-loading design a no-op.
func TestAlpineFragmentRegistryExcludesEngineAssets(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/component/chart", Kind: ModuleComponent, Name: "chart", Revision: 1, Contract: 1,
		Runtime: RuntimeContributions{Assets: []AssetContribution{
			{ID: "chartjs", Path: "static/vendor/chartjs-4.5.1.umd.min.js", Kind: AssetScript,
				Engine: "chartjs", Integrity: "sha256-AAAA"},
			{ID: "ui-chart", Path: "static/ui/chart.js", Kind: AssetScript},
		}},
	}}
	lock := Lock{Order: []string{"ggg/component/chart"}, Modules: []LockedModule{{ID: "ggg/component/chart"}}}

	f, err := emitAlpineFragments(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(f.Content, "/static/ui/chart.js") {
		t.Fatalf("the adapter is shell runtime and must load with the page:\n%s", f.Content)
	}
	if strings.Contains(f.Content, "chartjs-4.5.1") {
		t.Fatalf("an engine asset in the shell list defeats the whole lazy design:\n%s", f.Content)
	}
}

// Cally is an ES module that self-registers custom elements, and a module cannot
// be injected as a classic script: the browser rejects its export statement. The
// registry therefore carries the module flag, because only the manifest knows
// which build a vendor ships.
func TestEngineRegistryCarriesModuleFlag(t *testing.T) {
	mods := []Manifest{{
		ID: "ggg/component/calendar", Kind: ModuleComponent, Name: "calendar", Revision: 1, Contract: 1,
		Runtime: RuntimeContributions{Assets: []AssetContribution{{
			ID: "cally", Path: "static/vendor/cally-0.9.2.js", Kind: AssetScript,
			Engine: "cally", Integrity: "sha256-AAAA", ESM: true,
		}}},
	}}
	lock := Lock{Order: []string{"ggg/component/calendar"}, Modules: []LockedModule{{ID: "ggg/component/calendar"}}}

	f, err := emitEngineRegistry(context.Background(), "example.com/app", lock, mods)
	if err != nil {
		t.Fatalf("emit: %v", err)
	}
	if !strings.Contains(f.Content, "esm: true") {
		t.Fatalf("an ES module engine must be marked:\n%s", f.Content)
	}
}
