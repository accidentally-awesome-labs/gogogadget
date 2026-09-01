package modkit

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// removalRegistries builds a registry where page/optional is a free-removal
// module carrying one immutable migration, so removal can be exercised against
// the migration ledger.
func removalRegistries(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	first := plannerRegistry(t)
	optionalV1 := []byte("package optional\n\nconst Version = 1\n")
	optional := testLockedModule("ggg/page/optional", sha256Hex(optionalV1)).Manifest
	optional.Files = []ManifestFile{{
		Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
		Class: FileClassGo, SHA256: sha256Hex(optionalV1), Contract: true,
	}}
	addPlannerModule(t, first, optional, optionalV1)
	return first, nil
}

func installedRemovalProject(t *testing.T) (string, *Engine, fstest.MapFS) {
	t.Helper()
	first, _ := removalRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	return root, engine, first
}

func TestRemoveDeletesPristineModuleThroughPlan(t *testing.T) {
	t.Run("explicit module removal", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		before, err := os.ReadFile(filepath.Join(root, "internal/modules/optional.go"))
		if err != nil {
			t.Fatalf("read optional: %v", err)
		}
		plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
		if err != nil {
			t.Fatalf("Plan(remove): %v", err)
		}
		change := plannedChange(t, plan, "internal/modules/optional.go")
		if change.Kind != ChangeDelete || change.Class != DestinationAuthored || change.Module != "ggg/page/optional" {
			t.Fatalf("removal change = %#v", change)
		}
		if change.SHA256 != sha256Hex(before) {
			t.Fatalf("removal digest = %q, want pristine digest", change.SHA256)
		}
		if slices.Contains(plan.Project.Modules, "ggg/page/optional") || len(plan.Project.Exclude) != 0 {
			t.Fatalf("removal intent = modules %v exclude %v", plan.Project.Modules, plan.Project.Exclude)
		}
		intent := plannedChange(t, plan, "gogogadget.json")
		if intent.Class != DestinationIntent || intent.Kind != ChangeUpdate {
			t.Fatalf("intent change = %#v", intent)
		}
		var tombstone *LockedModule
		for i := range plan.Lock.Modules {
			if plan.Lock.Modules[i].ID == "ggg/page/optional" {
				tombstone = &plan.Lock.Modules[i]
			}
		}
		if tombstone == nil {
			t.Fatal("lock drops the removed module entirely; migration ledger would be lost")
		}
		if len(tombstone.Files) != 0 || tombstone.Pending != nil {
			t.Fatalf("tombstone = %#v", tombstone)
		}
		if !slices.Contains(plan.Lock.Order, "ggg/page/optional") {
			t.Fatalf("lock order omits tombstone: %v", plan.Lock.Order)
		}
		for _, module := range plan.Lock.Modules {
			if module.ID == "ggg/component/card" && module.SourceCommit != testCommitA {
				t.Fatalf("remaining module advanced: %#v", module)
			}
		}
		if _, err := os.Stat(filepath.Join(root, "internal/modules/optional.go")); err != nil {
			t.Fatalf("Plan deleted files from disk: %v", err)
		}
		if slices.Contains(plan.Resolved, "ggg/page/optional") || !slices.Contains(plan.Order, "ggg/page/optional") {
			t.Fatalf("removal resolved/order = %v / %v", plan.Resolved, plan.Order)
		}
	})

	t.Run("profile-supplied member moves to exclude", func(t *testing.T) {
		first, _ := removalRegistries(t)
		putJSON(t, first, "registry/profiles/full.json", ProfileDocument{
			Schema: 2,
			Profile: Profile{
				ID: "ggg/profile/full", Kind: CatalogProfile, Name: "full", Revision: 1, Contract: 1,
				Title: "Full", Description: "Full catalog.",
				Members: []string{"ggg/component/card", "ggg/element/button", "ggg/page/optional"},
			},
		})
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/profile/full"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
		if err != nil {
			t.Fatalf("Plan(remove): %v", err)
		}
		if !slices.Equal(plan.Project.Modules, []string{"ggg/profile/full"}) {
			t.Fatalf("modules mutated: %v", plan.Project.Modules)
		}
		if !slices.Equal(plan.Project.Exclude, []string{"ggg/page/optional"}) {
			t.Fatalf("exclude = %v, want [page/optional]", plan.Project.Exclude)
		}
	})
}

func TestRemoveRefusesUnsafeRemovals(t *testing.T) {
	t.Run("reverse dependency", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/element/button"}})
		if err == nil || !strings.Contains(err.Error(), "required by") {
			t.Fatalf("Plan error = %v, want reverse-dependency refusal", err)
		}
	})

	t.Run("modified owned file", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		writeTestFile(t, root, "internal/modules/optional.go", []byte("local edit"))
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
		if err == nil || !strings.Contains(err.Error(), "ggg diff ggg/page/optional") {
			t.Fatalf("Plan error = %v, want modified-file refusal naming ggg diff", err)
		}
	})

	t.Run("missing owned file", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		if err := os.Remove(filepath.Join(root, "internal/modules/optional.go")); err != nil {
			t.Fatalf("remove optional: %v", err)
		}
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("Plan error = %v, want missing-file refusal", err)
		}
	})

	t.Run("unknown and empty requests", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove}); err == nil || !strings.Contains(err.Error(), "at least one") {
			t.Fatalf("Plan(remove none) error = %v", err)
		}
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/missing"}}); err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("Plan(remove unknown) error = %v", err)
		}
	})

	t.Run("replacement-required and major-version-only policies", func(t *testing.T) {
		root, engine, registry := installedRemovalProject(t)
		_ = registry
		lockData, err := os.ReadFile(filepath.Join(root, "gogogadget.lock.json"))
		if err != nil {
			t.Fatalf("read lock: %v", err)
		}
		lock, err := ParseLock(lockData)
		if err != nil {
			t.Fatalf("ParseLock: %v", err)
		}
		for i := range lock.Modules {
			if lock.Modules[i].ID == "ggg/element/button" {
				lock.Modules[i].Manifest.RemovalPolicy = RemovalReplacementRequired
			}
			if lock.Modules[i].ID == "ggg/page/optional" {
				lock.Modules[i].Manifest.RemovalPolicy = RemovalMajorVersionOnly
			}
		}
		mutated, err := MarshalLock(lock)
		if err != nil {
			t.Fatalf("MarshalLock: %v", err)
		}
		writeTestFile(t, root, "gogogadget.lock.json", mutated)
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}}); err == nil || !strings.Contains(err.Error(), "major") {
			t.Fatalf("Plan(major-only) error = %v", err)
		}
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/component/card", "ggg/element/button"}}); err == nil || !strings.Contains(err.Error(), "replacement") {
			t.Fatalf("Plan(replacement) error = %v", err)
		}
	})

	t.Run("registry ref and missing lock refusals", func(t *testing.T) {
		root, engine, _ := installedRemovalProject(t)
		if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}, RegistryRef: "v2"}); err == nil || !strings.Contains(err.Error(), "registry ref") {
			t.Fatalf("Plan(remove with ref) error = %v", err)
		}
		bare := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card"}, Exclude: []string{},
		})
		if _, err := engine.Plan(context.Background(), bare, Operation{Kind: OpRemove, Modules: []string{"ggg/component/card"}}); err == nil || !strings.Contains(err.Error(), "existing gogogadget.lock.json") {
			t.Fatalf("Plan(remove without lock) error = %v", err)
		}
	})
}

func TestRemoveRetainsMigrationLedger(t *testing.T) {
	first, _ := removalRegistries(t)
	migrationContent := []byte("-- optional forward\nSELECT 1;\n")
	migrationSource := "registry/modules/page/optional/migrations/optional-forward.sql"
	first[migrationSource] = &fstest.MapFile{Data: migrationContent}
	mutatePlannerModule(t, first, "ggg/page/optional", func(module *Manifest) {
		module.Migrations = []ManifestMigration{{
			ID: "optional-forward", Kind: MigrationImmutable, Source: migrationSource, SHA256: sha256Hex(migrationContent),
		}}
	})
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	migrationPath := "internal/db/migrations/0001_optional_forward.sql"
	if _, err := os.Stat(filepath.Join(root, migrationPath)); err != nil {
		t.Fatalf("initial migration missing: %v", err)
	}

	remove, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove): %v", err)
	}
	for _, change := range remove.Changes {
		if change.Path == migrationPath {
			t.Fatalf("removal deleted retained migration: %#v", change)
		}
	}
	var tombstone *LockedModule
	for i := range remove.Lock.Modules {
		if remove.Lock.Modules[i].ID == "ggg/page/optional" {
			tombstone = &remove.Lock.Modules[i]
		}
	}
	if tombstone == nil || len(tombstone.Migrations) != 1 || tombstone.Migrations[0].Number != 1 {
		t.Fatalf("tombstone migrations = %#v", tombstone)
	}
	materializeConflictPlan(t, root, remove)
	// The real workflow runs sync between removal and re-add (make generate,
	// make check): the tombstone must survive it with its ledger intact.
	synced, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(sync after removal): %v", err)
	}
	var syncedTombstone *LockedModule
	for i := range synced.Lock.Modules {
		if synced.Lock.Modules[i].ID == "ggg/page/optional" {
			syncedTombstone = &synced.Lock.Modules[i]
		}
	}
	if syncedTombstone == nil || syncedTombstone.Reason != TombstoneReason ||
		len(syncedTombstone.Migrations) != 1 || syncedTombstone.Migrations[0].Number != 1 {
		t.Fatalf("sync dropped or damaged the tombstone: %#v", syncedTombstone)
	}
	if slices.Contains(synced.Resolved, "ggg/page/optional") || !slices.Contains(synced.Lock.Order, "ggg/page/optional") {
		t.Fatalf("post-removal sync resolved/order = %v / %v", synced.Resolved, synced.Lock.Order)
	}
	materializeConflictPlan(t, root, synced)
	readd, err := engine.Plan(context.Background(), root, Operation{Kind: OpAdd, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(re-add): %v", err)
	}
	ledger := plannedChange(t, readd, migrationPath)
	if ledger.Kind != ChangeUnchanged || ledger.Class != DestinationMigration {
		t.Fatalf("re-added migration change = %#v, want unchanged at the retained number", ledger)
	}
	for _, module := range readd.Lock.Modules {
		if module.ID == "ggg/page/optional" && (len(module.Migrations) != 1 || module.Migrations[0].Number != 1) {
			t.Fatalf("re-added migrations = %#v", module.Migrations)
		}
	}
}

func TestRemoveDrainRequiredMaterializesMigrations(t *testing.T) {
	drainRegistries := func(t *testing.T, withNeutralize, withPurge bool) fstest.MapFS {
		t.Helper()
		first, _ := removalRegistries(t)
		drainV1 := []byte("package drain\n")
		drain := testLockedModule("ggg/workflow/drain", sha256Hex(drainV1)).Manifest
		drain.RemovalPolicy = RemovalDrainRequired
		drain.Files = []ManifestFile{{
			Source: "registry/modules/workflow/drain/drain.go", Target: "internal/modules/drain.go",
			Class: FileClassGo, SHA256: sha256Hex(drainV1),
		}}
		if withNeutralize {
			content := []byte("-- neutralize schedules\nUPDATE schedules SET enabled = false;\n")
			source := "registry/modules/workflow/drain/migrations/drain-neutralize.sql"
			first[source] = &fstest.MapFile{Data: content}
			drain.Migrations = append(drain.Migrations, ManifestMigration{
				ID: "drain-neutralize", Kind: MigrationNeutralize, Source: source, SHA256: sha256Hex(content),
			})
		}
		if withPurge {
			content := []byte("-- purge drain rows\nDELETE FROM drain_rows;\n")
			source := "registry/modules/workflow/drain/migrations/drain-purge.sql"
			first[source] = &fstest.MapFile{Data: content}
			drain.Migrations = append(drain.Migrations, ManifestMigration{
				ID: "drain-purge", Kind: MigrationPurge, Source: source, SHA256: sha256Hex(content),
			})
		}
		addPlannerModule(t, first, drain, drainV1)
		return first
	}

	t.Run("refuses without neutralization migration", func(t *testing.T) {
		first := drainRegistries(t, false, false)
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/workflow/drain"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		_, err = engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}})
		if err == nil || !strings.Contains(err.Error(), "drain-required") {
			t.Fatalf("Plan error = %v, want drain-required refusal naming neutralization", err)
		}
	})

	t.Run("materializes neutralization and optional purge", func(t *testing.T) {
		first := drainRegistries(t, true, true)
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/workflow/drain"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)

		remove, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}})
		if err != nil {
			t.Fatalf("Plan(remove drain): %v", err)
		}
		neutralize := plannedChange(t, remove, "internal/db/migrations/0001_drain_neutralize.sql")
		if neutralize.Class != DestinationMigration || neutralize.Kind != ChangeCreate {
			t.Fatalf("neutralization change = %#v", neutralize)
		}
		if !strings.Contains(string(neutralize.Content), "UPDATE schedules") {
			t.Fatalf("neutralization payload = %q", neutralize.Content)
		}
		for _, change := range remove.Changes {
			if strings.HasSuffix(change.Path, "drain_purge.sql") {
				t.Fatalf("purge materialized without --purge-data: %#v", change)
			}
		}
		var tombstone *LockedModule
		for i := range remove.Lock.Modules {
			if remove.Lock.Modules[i].ID == "ggg/workflow/drain" {
				tombstone = &remove.Lock.Modules[i]
			}
		}
		if tombstone == nil || len(tombstone.Migrations) != 1 || tombstone.Migrations[0].Number != 1 {
			t.Fatalf("drain tombstone migrations = %#v", tombstone)
		}
		materializeConflictPlan(t, root, remove)

		_, err = engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}})
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("Plan(remove twice) error = %v, want already-removed refusal", err)
		}
	})

	t.Run("offline drain removal refuses before fetching", func(t *testing.T) {
		first := drainRegistries(t, true, false)
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/workflow/drain"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		_, err = engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}, Offline: true})
		if err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("Plan(offline drain) error = %v, want offline refusal", err)
		}
	})

	t.Run("purge-data materializes teardown after neutralization", func(t *testing.T) {
		first := drainRegistries(t, true, true)
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/workflow/drain"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		remove, err := engine.Plan(context.Background(), root, Operation{
			Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}, PurgeData: true,
		})
		if err != nil {
			t.Fatalf("Plan(remove purge): %v", err)
		}
		neutralize := plannedChange(t, remove, "internal/db/migrations/0001_drain_neutralize.sql")
		purge := plannedChange(t, remove, "internal/db/migrations/0002_drain_purge.sql")
		if neutralize.Kind != ChangeCreate || purge.Kind != ChangeCreate || purge.Class != DestinationMigration {
			t.Fatalf("drain changes = %#v / %#v", neutralize, purge)
		}
		if !strings.Contains(string(purge.Content), "DELETE FROM drain_rows") {
			t.Fatalf("purge payload = %q", purge.Content)
		}
		var tombstone *LockedModule
		for i := range remove.Lock.Modules {
			if remove.Lock.Modules[i].ID == "ggg/workflow/drain" {
				tombstone = &remove.Lock.Modules[i]
			}
		}
		if tombstone == nil || len(tombstone.Migrations) != 2 ||
			tombstone.Migrations[0].Number != 1 || tombstone.Migrations[1].Number != 2 {
			t.Fatalf("purge tombstone migrations = %#v", tombstone.Migrations)
		}
	})

	t.Run("remove re-add remove reuses the allocated ledger", func(t *testing.T) {
		first := drainRegistries(t, true, false)
		source := refSource{snapshots: map[string]Snapshot{
			"main":      {Commit: testCommitA, FS: first},
			testCommitA: {Commit: testCommitA, FS: first},
		}}
		root := writeTargetProject(t, "example.com/acme/app", Project{
			Schema:     2,
			Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
			Modules: []string{"ggg/component/card", "ggg/workflow/drain"}, Exclude: []string{},
		})
		engine := New(Options{Source: source})
		initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(initial): %v", err)
		}
		materializeConflictPlan(t, root, initial)
		removePlan, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}})
		if err != nil {
			t.Fatalf("Plan(remove): %v", err)
		}
		materializeConflictPlan(t, root, removePlan)
		readd, err := engine.Plan(context.Background(), root, Operation{Kind: OpAdd, Modules: []string{"ggg/workflow/drain"}})
		if err != nil {
			t.Fatalf("Plan(re-add): %v", err)
		}
		materializeConflictPlan(t, root, readd)
		second, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/workflow/drain"}})
		if err != nil {
			t.Fatalf("Plan(remove again): %v", err)
		}
		var tombstone *LockedModule
		for i := range second.Lock.Modules {
			if second.Lock.Modules[i].ID == "ggg/workflow/drain" {
				tombstone = &second.Lock.Modules[i]
			}
		}
		if tombstone == nil || len(tombstone.Migrations) != 1 || tombstone.Migrations[0].Number != 1 {
			t.Fatalf("re-removed tombstone migrations = %#v", tombstone.Migrations)
		}
		if change := plannedChange(t, second, "internal/db/migrations/0001_drain_neutralize.sql"); change.Kind != ChangeUnchanged {
			t.Fatalf("retained drain migration change = %#v, want unchanged", change)
		}
	})
}

func TestResolveConflictKeepsTombstonesOutOfResolved(t *testing.T) {
	firstRegistry, secondRegistry := conflictRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"v1":        {Commit: testCommitA, FS: firstRegistry},
		"v2":        {Commit: testCommitB, FS: secondRegistry},
		testCommitA: {Commit: testCommitA, FS: firstRegistry},
		testCommitB: {Commit: testCommitB, FS: secondRegistry},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card", "ggg/element/button", "ggg/page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)

	remove, err := engine.Plan(context.Background(), root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove): %v", err)
	}
	materializeConflictPlan(t, root, remove)

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

	// Resolving one of two conflicts keeps the clone-lock branch.
	partial, err := engine.ResolveConflict(context.Background(), root, "ggg/element/button", "internal/modules/button.go", ResolutionAcceptUpstream)
	if err != nil {
		t.Fatalf("ResolveConflict(partial): %v", err)
	}
	if slices.Contains(partial.Resolved, "ggg/page/optional") || !slices.Contains(partial.Order, "ggg/page/optional") {
		t.Fatalf("partial resolved/order = %v / %v", partial.Resolved, partial.Order)
	}

	// Resolving the final conflict runs the recomputed-graph branch.
	final, err := engine.ResolveConflict(context.Background(), root, "ggg/element/button", "internal/modules/button_helper.go", ResolutionAcceptUpstream)
	if err != nil {
		t.Fatalf("ResolveConflict(final): %v", err)
	}
	if slices.Contains(final.Resolved, "ggg/page/optional") || !slices.Contains(final.Order, "ggg/page/optional") {
		t.Fatalf("final resolved/order = %v / %v", final.Resolved, final.Order)
	}
	if slices.Contains(final.Lock.Order, "ggg/page/optional") == false {
		t.Fatalf("final lock order omits tombstone: %v", final.Lock.Order)
	}
}
