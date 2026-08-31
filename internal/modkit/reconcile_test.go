package modkit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

type refSource struct {
	snapshots map[string]Snapshot
}

func (s refSource) Resolve(_ context.Context, _, ref string) (Snapshot, error) {
	snapshot, ok := s.snapshots[ref]
	if !ok && ref == "main" {
		snapshot, ok = s.snapshots["v1"]
		if !ok {
			for _, candidate := range s.snapshots {
				snapshot, ok = candidate, true
				break
			}
		}
	}
	if !ok {
		return Snapshot{}, fmt.Errorf("unknown test ref %q", ref)
	}
	return snapshot, nil
}

func cloneMapFS(source fstest.MapFS) fstest.MapFS {
	clone := make(fstest.MapFS, len(source))
	for name, file := range source {
		copied := *file
		copied.Data = append([]byte(nil), file.Data...)
		clone[name] = &copied
	}
	return clone
}

func conflictRegistries(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	first := plannerRegistry(t)
	optionalV1 := []byte("package optional\n\nconst Version = 1\n")
	optional := testLockedModule("ggg/page/optional", sha256Hex(optionalV1)).Manifest
	optional.Files = []ManifestFile{{
		Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
		Class: FileClassGo, SHA256: sha256Hex(optionalV1), Contract: true,
	}}
	addPlannerModule(t, first, optional, optionalV1)

	helperV1 := []byte("package button\n\nconst HelperVersion = 1\n")
	first["registry/modules/element/button/button_helper.go"] = &fstest.MapFile{Data: helperV1}
	mutatePlannerModule(t, first, "ggg/element/button", func(module *Manifest) {
		module.Files = append(module.Files, ManifestFile{
			Source: "registry/modules/element/button/button_helper.go",
			Target: "internal/modules/button_helper.go",
			Class:  FileClassGo, SHA256: sha256Hex(helperV1), RewriteModule: true,
		})
	})

	second := cloneMapFS(first)
	buttonV2 := []byte(`package button

import "github.com/gogogadget/gogogadget/internal/modkit"

const Upstream = 2
var _ = modkit.OpSync
`)
	second["registry/modules/element/button/button.go"].Data = buttonV2
	helperV2 := []byte("package button\n\nconst HelperVersion = 2\n")
	second["registry/modules/element/button/button_helper.go"].Data = helperV2
	mutatePlannerModule(t, second, "ggg/element/button", func(module *Manifest) {
		module.Revision = 2
		module.Contract = 2
		module.Files[0].SHA256 = sha256Hex(buttonV2)
		module.Files[1].SHA256 = sha256Hex(helperV2)
	})
	mutatePlannerModule(t, second, "ggg/component/card", func(module *Manifest) {
		module.Requires[0].Contract.Max = 2
	})

	optionalV2 := []byte("package optional\n\nconst Version = 2\n")
	second["registry/modules/page/optional/optional.go"].Data = optionalV2
	mutatePlannerModule(t, second, "ggg/page/optional", func(module *Manifest) {
		module.Revision = 2
		module.Files[0].SHA256 = sha256Hex(optionalV2)
	})
	return first, second
}

func materializeConflictPlan(t *testing.T, root string, plan Plan) {
	t.Helper()
	materializePlanFixture(t, root, plan)
	for _, staged := range plan.Staged {
		writeTestFile(t, root, staged.Path, staged.Content)
	}
}

type preparedConflict struct {
	root       string
	engine     *Engine
	plan       Plan
	localBytes []byte
}

func prepareConflictFixture(t *testing.T) preparedConflict {
	t.Helper()
	firstRegistry, secondRegistry := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"v1":        {Commit: testCommitA, FS: firstRegistry},
		"v2":        {Commit: testCommitB, FS: secondRegistry},
		testCommitB: {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	local := []byte("package button\n\nconst Local = true\n")
	writeTestFile(t, root, "internal/modules/button.go", local)
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	return preparedConflict{root: root, engine: engine, plan: update, localBytes: local}
}

func TestAddMutatesIntentThroughSyncPlan(t *testing.T) {
	registry := plannerRegistry(t)
	putJSON(t, registry, "registry/profiles/full.json", ProfileDocument{
		Schema: 2,
		Profile: Profile{
			ID: "ggg/profile/full", Kind: CatalogProfile, Name: "full", Revision: 1, Contract: 1,
			Title: "Full", Description: "Full catalog.", Members: []string{"ggg/component/card", "ggg/element/button"},
		},
	})
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/profile/full"}, Exclude: []string{"ggg/component/card"},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpAdd, Modules: []string{"ggg/component/card"}})
	if err != nil {
		t.Fatalf("Plan(add): %v", err)
	}
	if len(plan.Project.Exclude) != 0 || !slices.Contains(plan.Resolved, "ggg/component/card") {
		t.Fatalf("add project/resolved = exclude %v resolved %v", plan.Project.Exclude, plan.Resolved)
	}
	intent := plannedChange(t, plan, "gogogadget.json")
	if intent.Class != DestinationIntent || intent.Kind != ChangeUpdate {
		t.Fatalf("intent change = class %q kind %q", intent.Class, intent.Kind)
	}
	parsed, err := ParseProject(intent.Content)
	if err != nil {
		t.Fatalf("ParseProject(planned intent): %v", err)
	}
	if len(parsed.Exclude) != 0 {
		t.Fatalf("planned exclude = %v, want empty", parsed.Exclude)
	}
}

func TestAddIsIdempotentAcrossMixedRequests(t *testing.T) {
	registry := plannerRegistry(t)
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/element/button"}, Exclude: []string{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	plan, err := engine.Plan(context.Background(), root, Operation{
		Kind: OpAdd, Modules: []string{"ggg/component/card", "ggg/element/button"},
	})
	if err != nil {
		t.Fatalf("Plan(add mixed): %v", err)
	}
	if got, want := plan.Project.Modules, []string{"ggg/component/card", "ggg/element/button"}; !slices.Equal(got, want) {
		t.Fatalf("planned modules = %v, want %v", got, want)
	}
}

func TestUpdateStagesEditedModuleAndUpdatesIndependentModule(t *testing.T) {
	fixture := prepareConflictFixture(t)
	plan := fixture.plan
	if got, want := plan.RegistryCommit, testCommitB; got != want {
		t.Fatalf("registry commit = %q, want %q", got, want)
	}
	if got, want := plan.Project.Registries[0].Ref, "v2"; got != want {
		t.Fatalf("project ref = %q, want %q", got, want)
	}
	intent := plannedChange(t, plan, "gogogadget.json")
	if intent.Class != DestinationIntent || intent.Kind != ChangeUpdate {
		t.Fatalf("intent change = class %q kind %q", intent.Class, intent.Kind)
	}
	if got := plannedChange(t, plan, "internal/modules/optional.go").Kind; got != ChangeUpdate {
		t.Fatalf("independent optional change kind = %q, want update", got)
	}
	for _, change := range plan.Changes {
		if change.Path == "internal/modules/button.go" || change.Path == "internal/web/templates/ui/card.templ" {
			t.Fatalf("held module path %s appears in authored changes", change.Path)
		}
	}
	if got, want := len(plan.Conflicts), 1; got != want {
		t.Fatalf("conflict count = %d, want %d", got, want)
	}
	conflict := plan.Conflicts[0]
	if conflict.Module != "ggg/element/button" || conflict.Path != "internal/modules/button.go" {
		t.Fatalf("conflict = %#v", conflict)
	}
	if got, want := len(plan.Staged), 2; got != want {
		t.Fatalf("staged file count = %d, want %d", got, want)
	}
	var candidate, diff StagedFile
	for _, staged := range plan.Staged {
		switch staged.Path {
		case conflict.CandidatePath:
			candidate = staged
		case conflict.DiffPath:
			diff = staged
		}
	}
	if candidate.Path == "" || diff.Path == "" {
		t.Fatalf("staged files do not match conflict metadata: %#v / %#v", plan.Staged, conflict)
	}
	if !strings.Contains(string(candidate.Content), `"example.com/acme/app/internal/modkit"`) {
		t.Fatalf("candidate import is not rewritten:\n%s", candidate.Content)
	}
	for _, marker := range []string{"--- ", "+++ ", "@@ "} {
		if !strings.Contains(string(diff.Content), marker) {
			t.Fatalf("unified diff lacks %q:\n%s", marker, diff.Content)
		}
	}
	local, err := os.ReadFile(filepath.Join(fixture.root, "internal/modules/button.go"))
	if err != nil {
		t.Fatalf("read local button: %v", err)
	}
	if !slices.Equal(local, fixture.localBytes) {
		t.Fatalf("Plan changed local button: %q", local)
	}

	commits := map[string]string{}
	for _, module := range plan.Lock.Modules {
		commits[module.ID] = module.SourceCommit
		if module.ID == "ggg/element/button" && module.Pending == nil {
			t.Fatal("conflicted button has no pending target")
		}
	}
	if commits["ggg/element/button"] != testCommitA || commits["ggg/component/card"] != testCommitA {
		t.Fatalf("held commits = button %q card %q, want old %q", commits["ggg/element/button"], commits["ggg/component/card"], testCommitA)
	}
	if commits["ggg/page/optional"] != testCommitB {
		t.Fatalf("independent commit = %q, want %q", commits["ggg/page/optional"], testCommitB)
	}

	repeated, err := fixture.engine.Plan(context.Background(), fixture.root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(repeated update): %v", err)
	}
	if !reflect.DeepEqual(plan, repeated) {
		t.Fatal("conflict plan is not deterministic")
	}
	materializeConflictPlan(t, fixture.root, plan)
	afterCommit, err := fixture.engine.Plan(context.Background(), fixture.root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(after conflict lock commit): %v", err)
	}
	if got, want := afterCommit.Conflicts[0].CandidatePath, conflict.CandidatePath; got != want {
		t.Fatalf("candidate path churned after committing pending lock: %q, want %q", got, want)
	}
}

func TestUpdateTreatsMatchingLocalAndUpstreamAsClean(t *testing.T) {
	firstRegistry, secondRegistry := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"v1": {Commit: testCommitA, FS: firstRegistry},
		"v2": {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)

	upstreamSource := secondRegistry["registry/modules/element/button/button.go"].Data
	upstreamInstalled, err := rewriteModuleImports(
		"internal/modules/button.go", upstreamSource,
		"github.com/gogogadget/gogogadget", "example.com/acme/app",
	)
	if err != nil {
		t.Fatalf("rewrite upstream fixture: %v", err)
	}
	writeTestFile(t, root, "internal/modules/button.go", upstreamInstalled)
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	if len(update.Conflicts) != 0 {
		t.Fatalf("matching local/upstream bytes produced conflicts: %#v", update.Conflicts)
	}
	for _, change := range update.Changes {
		if change.Path == "internal/modules/button.go" {
			t.Fatalf("matching local/upstream bytes produced a write: %#v", change)
		}
	}
	for _, module := range update.Lock.Modules {
		if module.ID == "ggg/element/button" {
			if module.SourceCommit != testCommitB || module.Files[0].State != FileClean {
				t.Fatalf("converged button source/state = %q/%q", module.SourceCommit, module.Files[0].State)
			}
		}
	}
}

func TestSyncRefusesImplicitInstalledModuleRemoval(t *testing.T) {
	firstRegistry, _ := conflictRegistries(t)
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: firstRegistry}}})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	intent, err := MarshalProject(Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	writeTestFile(t, root, "gogogadget.json", intent)
	_, err = engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil || !strings.Contains(err.Error(), "removal planning") {
		t.Fatalf("Plan error = %v, want implicit removal refusal", err)
	}
}

func TestResolveConflictModes(t *testing.T) {
	tests := []struct {
		name string
		mode ResolutionMode
	}{
		{name: "accept upstream", mode: ResolutionAcceptUpstream},
		{name: "keep local", mode: ResolutionKeepLocal},
		{name: "merged", mode: ResolutionMerged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := prepareConflictFixture(t)
			materializeConflictPlan(t, fixture.root, fixture.plan)
			merged := []byte("package button\n\nconst Merged = true\n")
			if tt.mode == ResolutionMerged {
				writeTestFile(t, fixture.root, "internal/modules/button.go", merged)
			}
			plan, err := fixture.engine.ResolveConflict(
				context.Background(), fixture.root, "ggg/element/button", "internal/modules/button.go", tt.mode,
			)
			if err != nil {
				t.Fatalf("ResolveConflict: %v", err)
			}
			var button LockedModule
			for _, module := range plan.Lock.Modules {
				if module.ID == "ggg/element/button" {
					button = module
				}
			}
			if button.SourceCommit != testCommitB || button.Pending != nil {
				t.Fatalf("resolved button source/pending = %q/%#v", button.SourceCommit, button.Pending)
			}
			if tt.mode == ResolutionAcceptUpstream {
				change := plannedChange(t, plan, "internal/modules/button.go")
				if change.Kind != ChangeUpdate || !strings.Contains(string(change.Content), "Upstream = 2") {
					t.Fatalf("accept-upstream change = %#v", change)
				}
				if button.Files[0].State != FileClean {
					t.Fatalf("accepted file state = %q, want clean", button.Files[0].State)
				}
			} else {
				for _, change := range plan.Changes {
					if change.Path == "internal/modules/button.go" {
						t.Fatalf("%s unexpectedly writes local button", tt.name)
					}
				}
				if button.Files[0].State != FileModified {
					t.Fatalf("kept file state = %q, want modified", button.Files[0].State)
				}
				wantLocal := fixture.localBytes
				if tt.mode == ResolutionMerged {
					wantLocal = merged
				}
				if button.Files[0].LocalSHA256 != sha256Hex(wantLocal) {
					t.Fatalf("kept local sha = %q, want %q", button.Files[0].LocalSHA256, sha256Hex(wantLocal))
				}
			}
		})
	}
}

func TestConflictPlanDoesNotWriteStaging(t *testing.T) {
	fixture := prepareConflictFixture(t)
	for _, staged := range fixture.plan.Staged {
		if _, err := os.Stat(filepath.Join(fixture.root, filepath.FromSlash(staged.Path))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Plan wrote staged path %s: %v", staged.Path, err)
		}
	}
}

func TestSyncRestoresMissingOwnedFile(t *testing.T) {
	firstRegistry, secondRegistry := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"v1": {Commit: testCommitA, FS: firstRegistry},
		"v2": {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	if err := os.Remove(filepath.Join(root, "internal/modules/optional.go")); err != nil {
		t.Fatalf("remove optional: %v", err)
	}
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update with missing file): %v", err)
	}
	change := plannedChange(t, update, "internal/modules/optional.go")
	if change.Kind != ChangeCreate || !strings.Contains(string(change.Content), "Version = 2") {
		t.Fatalf("missing-file restore change = %#v", change)
	}
	for _, module := range update.Lock.Modules {
		if module.ID == "ggg/page/optional" && module.Files[0].State != FileClean {
			t.Fatalf("restored file state = %q, want clean", module.Files[0].State)
		}
	}
}

func TestUpdateHandlesUpstreamDroppedFile(t *testing.T) {
	t.Run("pristine drop deletes", func(t *testing.T) {
		firstRegistry, secondRegistry := conflictRegistries(t)
		mutatePlannerModule(t, secondRegistry, "ggg/page/optional", func(module *Manifest) {
			module.Files = []ManifestFile{}
			module.Revision = 2
		})
		source := refSource{snapshots: map[string]Snapshot{
			"v1": {Commit: testCommitA, FS: firstRegistry},
			"v2": {Commit: testCommitB, FS: secondRegistry},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
		if err != nil {
			t.Fatalf("Plan(update): %v", err)
		}
		change := plannedChange(t, update, "internal/modules/optional.go")
		if change.Kind != ChangeDelete || change.Class != DestinationAuthored {
			t.Fatalf("dropped-file change = %#v", change)
		}
		for _, module := range update.Lock.Modules {
			if module.ID == "ggg/page/optional" && len(module.Files) != 0 {
				t.Fatalf("dropped file remains locked: %#v", module.Files)
			}
		}
	})

	t.Run("modified drop refuses", func(t *testing.T) {
		firstRegistry, secondRegistry := conflictRegistries(t)
		mutatePlannerModule(t, secondRegistry, "ggg/page/optional", func(module *Manifest) {
			module.Files = []ManifestFile{}
			module.Revision = 2
		})
		source := refSource{snapshots: map[string]Snapshot{
			"v1": {Commit: testCommitA, FS: firstRegistry},
			"v2": {Commit: testCommitB, FS: secondRegistry},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		writeTestFile(t, root, "internal/modules/optional.go", []byte("local edit"))
		_, err = engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
		if err == nil || !strings.Contains(err.Error(), "removal planning is required") {
			t.Fatalf("Plan error = %v, want modified-drop refusal", err)
		}
	})
}

func TestResolveFinalClearAllocatesTargetMigration(t *testing.T) {
	firstRegistry, secondRegistry := conflictRegistries(t)
	migrationContent := []byte("-- resolved forward\nSELECT 1;\n")
	migrationSource := "registry/modules/element/button/migrations/button-forward.sql"
	secondRegistry[migrationSource] = &fstest.MapFile{Data: migrationContent}
	mutatePlannerModule(t, secondRegistry, "ggg/element/button", func(module *Manifest) {
		module.Migrations = []ManifestMigration{{
			ID: "button-forward", Kind: MigrationImmutable, Source: migrationSource, SHA256: sha256Hex(migrationContent),
		}}
	})
	source := refSource{snapshots: map[string]Snapshot{
		"v1":        {Commit: testCommitA, FS: firstRegistry},
		"v2":        {Commit: testCommitB, FS: secondRegistry},
		testCommitB: {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	writeTestFile(t, root, "internal/modules/button.go", []byte("package button\n\nconst Local = true\n"))
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	materializeConflictPlan(t, root, update)
	plan, err := engine.ResolveConflict(
		context.Background(), root, "ggg/element/button", "internal/modules/button.go", ResolutionAcceptUpstream,
	)
	if err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	migration := plannedChange(t, plan, "internal/db/migrations/0001_button_forward.sql")
	if migration.Class != DestinationMigration {
		t.Fatalf("resolve migration change class = %q", migration.Class)
	}
	for _, module := range plan.Lock.Modules {
		if module.ID == "ggg/element/button" {
			if len(module.Migrations) != 1 || module.Migrations[0].Number != 1 {
				t.Fatalf("resolved migrations = %#v", module.Migrations)
			}
			if module.Pending != nil {
				t.Fatal("resolved button still pending")
			}
		}
	}
}

func TestPartialResolutionVerifiesFreshPayload(t *testing.T) {
	firstRegistry, secondRegistry := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"v1":        {Commit: testCommitA, FS: firstRegistry},
		"v2":        {Commit: testCommitB, FS: secondRegistry},
		testCommitB: {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	writeTestFile(t, root, "internal/modules/button.go", []byte("package button\n\nconst LocalA = true\n"))
	writeTestFile(t, root, "internal/modules/button_helper.go", []byte("package button\n\nconst LocalB = true\n"))
	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	if got, want := len(update.Conflicts), 2; got != want {
		t.Fatalf("conflict count = %d, want %d", got, want)
	}
	materializeConflictPlan(t, root, update)

	tampered := []byte("tampered candidate bytes")
	lockChange := plannedChange(t, update, "gogogadget.lock.json")
	lock, err := ParseLock(lockChange.Content)
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}
	for i := range lock.Modules {
		module := &lock.Modules[i]
		if module.ID != "ggg/element/button" || module.Pending == nil {
			continue
		}
		for j := range module.Pending.Conflicts {
			if module.Pending.Conflicts[j].Path == "internal/modules/button_helper.go" {
				module.Pending.Conflicts[j].CandidateSHA256 = sha256Hex(tampered)
				writeTestFile(t, root, module.Pending.Conflicts[j].CandidatePath, tampered)
			}
		}
	}
	tamperedLock, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock(tampered): %v", err)
	}
	writeTestFile(t, root, "gogogadget.lock.json", tamperedLock)
	_, err = engine.ResolveConflict(
		context.Background(), root, "ggg/element/button", "internal/modules/button_helper.go", ResolutionAcceptUpstream,
	)
	if err == nil || !strings.Contains(err.Error(), "candidate sha256 does not match target manifest payload") {
		t.Fatalf("ResolveConflict error = %v, want fresh payload mismatch", err)
	}
}

// Adoption runs against a tree that already contains files. A pre-existing file
// whose bytes differ from the registry payload is unowned: overwriting it would
// destroy work that predates the registry, and recording it as clean would lie
// about what is installed. It blocks adoption until the operator claims it.
func TestAdoptionRefusesUnclaimedDivergentFile(t *testing.T) {
	first, _ := removalRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/optional"}, Exclude: []string{},
	})
	// The operator already had this file, with their own contents.
	local := []byte("package optional\n\n// hand-written before adoption\nconst Version = 99\n")
	writeTestFile(t, root, "internal/modules/optional.go", local)

	engine := New(Options{Source: source})

	_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil {
		t.Fatal("Plan adopted a divergent pre-existing file without a claim")
	}
	for _, want := range []string{"internal/modules/optional.go", "--claim"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not mention %q", err, want)
		}
	}

	// Claiming it adopts the local bytes as a modification, never overwriting.
	plan, err := engine.Plan(context.Background(), root, Operation{
		Kind:   OpSync,
		Claims: []string{"internal/modules/optional.go"},
	})
	if err != nil {
		t.Fatalf("Plan(claimed): %v", err)
	}
	change := plannedChange(t, plan, "internal/modules/optional.go")
	if change.Kind != ChangeUnchanged {
		t.Fatalf("claimed file change = %q, want %q so local bytes survive", change.Kind, ChangeUnchanged)
	}

	var locked *LockedFile
	for _, module := range plan.Lock.Modules {
		for i := range module.Files {
			if module.Files[i].Path == "internal/modules/optional.go" {
				locked = &module.Files[i]
			}
		}
	}
	if locked == nil {
		t.Fatal("claimed file is absent from the lock")
	}
	if locked.State != FileModified {
		t.Fatalf("claimed file state = %q, want %q", locked.State, FileModified)
	}
	if locked.LocalSHA256 != digestBytes(local) {
		t.Fatalf("local_sha256 = %q, want the digest of the operator's bytes", locked.LocalSHA256)
	}
	if locked.BaseSHA256 == locked.LocalSHA256 {
		t.Fatal("base_sha256 equals local_sha256; the upstream digest was not recorded")
	}
}
