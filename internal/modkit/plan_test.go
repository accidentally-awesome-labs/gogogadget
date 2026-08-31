package modkit

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

type staticSource struct {
	snapshot Snapshot
	err      error
}

func (s staticSource) Resolve(context.Context, string, string) (Snapshot, error) {
	return s.snapshot, s.err
}

func plannerRegistry(t *testing.T) fstest.MapFS {
	t.Helper()
	files := registryFixture(t)
	buttonContent := []byte(`package button

import "github.com/gogogadget/gogogadget/internal/modkit"

// CanonicalPath is documentation, not an import.
const CanonicalPath = "github.com/gogogadget/gogogadget/not-an-import"

var _ = modkit.OpSync
`)
	button := testLockedModule("ggg/element/button", sha256Hex(buttonContent)).Manifest
	button.Files = []ManifestFile{{
		Source: "registry/modules/element/button/button.go", Target: "internal/modules/button.go",
		Class: FileClassGo, SHA256: sha256Hex(buttonContent), RewriteModule: true, Contract: true,
	}}
	putJSON(t, files, "registry/modules/element/button/module.json", ModuleDocument{Schema: 2, Module: button})
	files[button.Files[0].Source] = &fstest.MapFile{Data: buttonContent}

	cardContent := []byte(`package ui

import (
	"github.com/gogogadget/gogogadget/internal/modules/button"
)

templ Card() {
	<div data-source="github.com/gogogadget/gogogadget/not-an-import">Card</div>
}
`)
	card := testLockedModule("ggg/component/card", sha256Hex(cardContent)).Manifest
	card.Requires = []Requirement{{ID: "ggg/element/button", Contract: ContractBounds{Min: 1, Max: 1}}}
	card.Files = []ManifestFile{{
		Source: "registry/modules/component/card/card.templ", Target: "internal/web/templates/ui/card.templ",
		Class: FileClassTempl, SHA256: sha256Hex(cardContent), RewriteModule: true, Contract: true,
	}}
	putJSON(t, files, "registry/modules/component/card/module.json", ModuleDocument{Schema: 2, Module: card})
	files[card.Files[0].Source] = &fstest.MapFile{Data: cardContent}
	putJSON(t, files, "registry/components.json", CatalogIndex{
		Schema: 2, Kind: CatalogComponent, Items: []string{"registry/modules/component/card/module.json"},
	})
	return files
}

func mutatePlannerModule(t *testing.T, files fstest.MapFS, id string, mutate func(*Manifest)) {
	t.Helper()
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		t.Fatalf("invalid module id %q", id)
	}
	kind, name, ok := parts[1], parts[2], true
	if !ok {
		t.Fatalf("invalid module id %q", id)
	}
	path := "registry/modules/" + kind + "/" + name + "/module.json"
	entry, ok := files[path]
	if !ok {
		t.Fatalf("missing fixture %s", path)
	}
	var document ModuleDocument
	if err := json.Unmarshal(entry.Data, &document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	mutate(&document.Module)
	putJSON(t, files, path, document)
}

func writeTargetProject(t *testing.T, modulePath string, project Project) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module "+modulePath+"\n\ngo 1.26.6\n"))
	data, err := MarshalProject(project)
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	writeTestFile(t, root, "gogogadget.json", data)
	return root
}

func plannedChange(t *testing.T, plan Plan, path string) Change {
	t.Helper()
	for _, change := range plan.Changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("plan has no change for %s", path)
	return Change{}
}

func addPlannerModule(t *testing.T, files fstest.MapFS, manifest Manifest, content []byte) {
	t.Helper()
	if len(manifest.Files) != 1 {
		t.Fatalf("test module %s must have one file", manifest.ID)
	}
	kind := string(manifest.Kind)
	itemPath := "registry/modules/" + kind + "/" + manifest.Name + "/module.json"
	putJSON(t, files, itemPath, ModuleDocument{Schema: 2, Module: manifest})
	files[manifest.Files[0].Source] = &fstest.MapFile{Data: content}

	indexPath := "registry/" + kind + "s.json"
	var index CatalogIndex
	if err := json.Unmarshal(files[indexPath].Data, &index); err != nil {
		t.Fatalf("decode %s: %v", indexPath, err)
	}
	index.Items = append(index.Items, itemPath)
	slices.Sort(index.Items)
	putJSON(t, files, indexPath, index)
}

func materializePlanFixture(t *testing.T, root string, plan Plan) {
	t.Helper()
	for _, change := range plan.Changes {
		switch change.Kind {
		case ChangeUnchanged:
			continue
		case ChangeDelete:
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(change.Path))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("remove %s: %v", change.Path, err)
			}
		default:
			writeTestFile(t, root, change.Path, change.Content)
		}
	}
}

func TestEnginePlanResolvesClosureAndRewritesImportsWithoutWriting(t *testing.T) {
	registry := plannerRegistry(t)
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"},
		Exclude: []string{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})

	first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(first): %v", err)
	}
	if got, want := first.RegistryCommit, testCommitA; got != want {
		t.Fatalf("registry commit = %q, want %q", got, want)
	}
	if got, want := first.ModulePath, "example.com/acme/app"; got != want {
		t.Fatalf("module path = %q, want %q", got, want)
	}
	wantOrder := []string{"ggg/element/button", "ggg/component/card"}
	if !slices.Equal(first.Order, wantOrder) || !slices.Equal(first.Resolved, wantOrder) {
		t.Fatalf("order/resolved = %v/%v, want %v", first.Order, first.Resolved, wantOrder)
	}

	button := plannedChange(t, first, "internal/modules/button.go")
	if button.Kind != ChangeCreate || button.Class != DestinationAuthored {
		t.Fatalf("button change = kind %q class %q", button.Kind, button.Class)
	}
	buttonText := string(button.Content)
	if !strings.Contains(buttonText, `"example.com/acme/app/internal/modkit"`) {
		t.Fatalf("button import was not rewritten:\n%s", buttonText)
	}
	if !strings.Contains(buttonText, `"github.com/gogogadget/gogogadget/not-an-import"`) {
		t.Fatalf("button non-import string was rewritten:\n%s", buttonText)
	}

	card := plannedChange(t, first, "internal/web/templates/ui/card.templ")
	cardText := string(card.Content)
	if !strings.Contains(cardText, `"example.com/acme/app/internal/modules/button"`) {
		t.Fatalf("templ import was not rewritten:\n%s", cardText)
	}
	if !strings.Contains(cardText, `data-source="github.com/gogogadget/gogogadget/not-an-import"`) {
		t.Fatalf("templ body string was rewritten:\n%s", cardText)
	}

	lockChange := plannedChange(t, first, "gogogadget.lock.json")
	if lockChange.Class != DestinationLock {
		t.Fatalf("lock class = %q, want %q", lockChange.Class, DestinationLock)
	}
	lock, err := ParseLock(lockChange.Content)
	if err != nil {
		t.Fatalf("ParseLock(planned): %v", err)
	}
	var lockedButton LockedModule
	for _, module := range lock.Modules {
		if module.ID == "ggg/element/button" {
			lockedButton = module
		}
	}
	if lockedButton.ID == "" {
		t.Fatal("planned lock omits element/button")
	}
	if lockedButton.Files[0].BaseSHA256 == lockedButton.Manifest.Files[0].SHA256 {
		t.Fatal("rewritten derivative base digest unexpectedly equals upstream source digest")
	}

	for _, path := range []string{"internal/modules/button.go", "internal/web/templates/ui/card.templ", "gogogadget.lock.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("Plan wrote %s: stat error = %v", path, err)
		}
	}

	second, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(second): %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("two plans differ:\nfirst: %#v\nsecond: %#v", first, second)
	}
}

func TestEnginePlanRejectsInvalidGraphsPayloadsAndNamespaces(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, fstest.MapFS)
		want   string
	}{
		{
			name: "dependency cycle",
			mutate: func(t *testing.T, files fstest.MapFS) {
				mutatePlannerModule(t, files, "ggg/element/button", func(module *Manifest) {
					module.Requires = []Requirement{{ID: "ggg/component/card", Contract: ContractBounds{Min: 1, Max: 1}}}
				})
			},
			want: "cycle",
		},
		{
			name: "payload digest",
			mutate: func(_ *testing.T, files fstest.MapFS) {
				files["registry/modules/element/button/button.go"].Data = []byte("changed")
			},
			want: "sha256",
		},
		{
			name: "owned target collision",
			mutate: func(t *testing.T, files fstest.MapFS) {
				mutatePlannerModule(t, files, "ggg/component/card", func(module *Manifest) {
					module.Files[0].Target = "internal/modules/button.go"
				})
			},
			want: "target",
		},
		{
			name: "GET HEAD wildcard route collision",
			mutate: func(t *testing.T, files fstest.MapFS) {
				mutatePlannerModule(t, files, "ggg/element/button", func(module *Manifest) {
					module.Runtime.Routes = []RouteContribution{{
						ID: "button.show", Method: "GET", Pattern: "/items/{id}", Scope: RoutePublic,
						Policy: RoutePolicy{}, Package: "example.com/acme/web", Handler: "Button",
					}}
				})
				mutatePlannerModule(t, files, "ggg/component/card", func(module *Manifest) {
					module.Runtime.Routes = []RouteContribution{{
						ID: "card.show", Method: "HEAD", Pattern: "/items/{slug}", Scope: RoutePublic,
						Policy: RoutePolicy{}, Package: "example.com/acme/web", Handler: "Card",
					}}
				})
			},
			want: "route",
		},
		{
			name: "environment claim collision",
			mutate: func(t *testing.T, files fstest.MapFS) {
				for _, id := range []string{"ggg/element/button", "ggg/component/card"} {
					mutatePlannerModule(t, files, id, func(module *Manifest) {
						module.Claims.Environment = []string{"SHARED_ENV"}
					})
				}
			},
			want: "environment",
		},
		{
			name: "content route collision",
			mutate: func(t *testing.T, files fstest.MapFS) {
				mutatePlannerModule(t, files, "ggg/element/button", func(module *Manifest) {
					module.Runtime.Routes = []RouteContribution{{
						ID: "blog.route", Method: "GET", Pattern: "/blog", Scope: RoutePublic,
						Policy: RoutePolicy{}, Package: "example.com/acme/web", Handler: "Blog",
					}}
				})
				mutatePlannerModule(t, files, "ggg/component/card", func(module *Manifest) {
					module.Runtime.ContentTypes = []ContentTypeContribution{{
						ID: "blog", Mode: ContentModePages, Paths: []string{"/blog"},
						Package: "example.com/acme/content", Handler: "Blog",
					}}
				})
			},
			want: "route",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := plannerRegistry(t)
			tt.mutate(t, registry)
			root := writeTargetProject(t, "example.com/acme/app", Project{
				Schema:     2,
				Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
				Modules: []string{"ggg/component/card"}, Exclude: []string{},
			})
			engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
			_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.want)) {
				t.Fatalf("Plan error = %v, want %q rejection", err, tt.want)
			}
		})
	}
}

func TestEnginePlanAllocatesMigrationNumbers(t *testing.T) {
	registry := plannerRegistry(t)
	migrationContent := []byte("-- forward-only\nSELECT 1;\n")
	migrationSource := "registry/modules/component/card/migrations/card-forward.sql"
	registry[migrationSource] = &fstest.MapFile{Data: migrationContent}
	mutatePlannerModule(t, registry, "ggg/component/card", func(module *Manifest) {
		module.Migrations = []ManifestMigration{{
			ID: "card-forward", Kind: MigrationImmutable, Source: migrationSource, SHA256: sha256Hex(migrationContent),
		}}
	})
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	})
	writeTestFile(t, root, "internal/db/migrations/0007_existing.sql", []byte("-- existing\n"))
	if err := os.MkdirAll(filepath.Join(root, "internal/db/migrations/0099_scratch.sql"), 0o755); err != nil {
		t.Fatalf("mkdir migration lookalike: %v", err)
	}
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})

	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	migration := plannedChange(t, plan, "internal/db/migrations/0008_card_forward.sql")
	if migration.Class != DestinationMigration || migration.Module != "ggg/component/card" {
		t.Fatalf("migration change = class %q module %q", migration.Class, migration.Module)
	}
	if !slices.Equal(migration.Content, migrationContent) {
		t.Fatalf("migration content = %q, want %q", migration.Content, migrationContent)
	}
	lockChange := plannedChange(t, plan, "gogogadget.lock.json")
	lock, err := ParseLock(lockChange.Content)
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}
	if got, want := lock.Modules[0].Migrations[0].Number, 8; got != want {
		t.Fatalf("migration number = %d, want %d", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/db/migrations/0008_card_forward.sql")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Plan wrote migration: %v", err)
	}
}

func TestEnginePlanProfileExclusionsAndReasons(t *testing.T) {
	registry := plannerRegistry(t)

	optionalContent := []byte("package optional\n")
	optional := testLockedModule("ggg/page/optional", sha256Hex(optionalContent)).Manifest
	optional.Files = []ManifestFile{{
		Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
		Class: FileClassGo, SHA256: sha256Hex(optionalContent),
	}}
	addPlannerModule(t, registry, optional, optionalContent)

	coreContent := []byte("package core\n")
	core := testLockedModule("ggg/system/core", sha256Hex(coreContent)).Manifest
	core.RemovalPolicy = RemovalReplacementRequired
	core.Files = []ManifestFile{{
		Source: "registry/modules/system/core/core.go", Target: "internal/modules/core.go",
		Class: FileClassGo, SHA256: sha256Hex(coreContent),
	}}
	addPlannerModule(t, registry, core, coreContent)

	putJSON(t, registry, "registry/profiles/full.json", ProfileDocument{
		Schema: 2,
		Profile: Profile{
			ID: "ggg/profile/full", Kind: CatalogProfile, Name: "full", Revision: 1, Contract: 1,
			Title: "Full", Description: "Every module.",
			Members: []string{"ggg/component/card", "ggg/element/button", "ggg/page/optional", "ggg/system/core"},
		},
	})
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/profile/full"},
		Exclude: []string{"ggg/element/button", "ggg/page/optional", "ggg/system/core"},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if slices.Contains(plan.Resolved, "ggg/page/optional") {
		t.Fatalf("excluded free module remains resolved: %v", plan.Resolved)
	}
	for _, id := range []string{"ggg/component/card", "ggg/element/button", "ggg/system/core"} {
		if !slices.Contains(plan.Resolved, id) {
			t.Fatalf("resolved modules %v omit %s", plan.Resolved, id)
		}
	}
	reasons := map[string]string{}
	for _, module := range plan.Lock.Modules {
		reasons[module.ID] = module.Reason
	}
	if got, want := reasons["ggg/component/card"], "profile"; got != want {
		t.Fatalf("card reason = %q, want %q", got, want)
	}
	if got, want := reasons["ggg/element/button"], "dependency"; got != want {
		t.Fatalf("button reason = %q, want %q", got, want)
	}
	if got, want := reasons["ggg/system/core"], "profile"; got != want {
		t.Fatalf("core reason = %q, want %q", got, want)
	}
}

func TestEnginePlanClassifiesDestinationStates(t *testing.T) {
	project := Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	}

	t.Run("unchanged and upstream update", func(t *testing.T) {
		registry := plannerRegistry(t)
		root := writeTargetProject(t, "example.com/acme/app", project)
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
		first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(first): %v", err)
		}
		materializePlanFixture(t, root, first)
		second, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(second): %v", err)
		}
		for _, change := range second.Changes {
			if change.Kind != ChangeUnchanged {
				t.Fatalf("second-plan change %s kind = %q, want unchanged", change.Path, change.Kind)
			}
		}

		updated := plannerRegistry(t)
		updatedContent := []byte("package button\n\nconst Updated = true\n")
		updated["registry/modules/element/button/button.go"].Data = updatedContent
		mutatePlannerModule(t, updated, "ggg/element/button", func(module *Manifest) {
			module.Files[0].SHA256 = sha256Hex(updatedContent)
			module.Files[0].RewriteModule = false
		})
		updateEngine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitB, FS: updated}}})
		update, err := updateEngine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(update): %v", err)
		}
		if got := plannedChange(t, update, "internal/modules/button.go").Kind; got != ChangeUpdate {
			t.Fatalf("upstream button change kind = %q, want update", got)
		}
	})

	t.Run("divergent pre-existing target blocks adoption", func(t *testing.T) {
		root := writeTargetProject(t, "example.com/acme/app", project)
		writeTestFile(t, root, "internal/modules/button.go", []byte("local"))
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: plannerRegistry(t)}}})
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err == nil {
			t.Fatal("Plan overwrote a divergent pre-existing file")
		}
		// The refusal must name the remedy: an operator with no next step will
		// reach for a force flag that deliberately does not exist.
		for _, want := range []string{"adoption blocked", "internal/modules/button.go", "--claim"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not mention %q", err, want)
			}
		}
	})

	t.Run("locally modified owned target with unchanged upstream", func(t *testing.T) {
		root := writeTargetProject(t, "example.com/acme/app", project)
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: plannerRegistry(t)}}})
		first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(first): %v", err)
		}
		materializePlanFixture(t, root, first)
		writeTestFile(t, root, "internal/modules/button.go", []byte("local edit"))
		second, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err != nil {
			t.Fatalf("Plan(second): %v", err)
		}
		for _, change := range second.Changes {
			if change.Path == "internal/modules/button.go" {
				t.Fatalf("unchanged upstream unexpectedly rewrites local edit: %#v", change)
			}
		}
		for _, module := range second.Lock.Modules {
			if module.ID == "ggg/element/button" && module.Files[0].State != FileModified {
				t.Fatalf("local edit state = %q, want modified", module.Files[0].State)
			}
		}
	})

	t.Run("symlink target refusal", func(t *testing.T) {
		root := writeTargetProject(t, "example.com/acme/app", project)
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
			t.Fatalf("mkdir internal: %v", err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "internal/modules")); err != nil {
			t.Fatalf("symlink modules: %v", err)
		}
		engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: plannerRegistry(t)}}})
		_, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("Plan error = %v, want symlink refusal", err)
		}
	})
}

func TestEnginePlanPreservesImmutableMigrationMapping(t *testing.T) {
	registry := plannerRegistry(t)
	migrationSource := "registry/modules/component/card/migrations/card-forward.sql"
	original := []byte("-- immutable\nSELECT 1;\n")
	registry[migrationSource] = &fstest.MapFile{Data: original}
	mutatePlannerModule(t, registry, "ggg/component/card", func(module *Manifest) {
		module.Migrations = []ManifestMigration{{
			ID: "card-forward", Kind: MigrationImmutable, Source: migrationSource, SHA256: sha256Hex(original),
		}}
	})
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	})
	writeTestFile(t, root, "internal/db/migrations/0007_existing.sql", []byte("-- existing\n"))
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(first): %v", err)
	}
	materializePlanFixture(t, root, first)
	second, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(second): %v", err)
	}
	migration := plannedChange(t, second, "internal/db/migrations/0008_card_forward.sql")
	if migration.Kind != ChangeUnchanged {
		t.Fatalf("preserved migration kind = %q, want unchanged", migration.Kind)
	}

	changed := plannerRegistry(t)
	changedContent := []byte("-- changed\nSELECT 2;\n")
	changed[migrationSource] = &fstest.MapFile{Data: changedContent}
	mutatePlannerModule(t, changed, "ggg/component/card", func(module *Manifest) {
		module.Migrations = []ManifestMigration{{
			ID: "card-forward", Kind: MigrationImmutable, Source: migrationSource, SHA256: sha256Hex(changedContent),
		}}
	})
	changedEngine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitB, FS: changed}}})
	_, err = changedEngine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil || !strings.Contains(err.Error(), "payload changed") {
		t.Fatalf("Plan(changed migration) error = %v, want immutable payload rejection", err)
	}
}

func TestEnginePlanRejectsRetainedMigrationTargetCollision(t *testing.T) {
	registry := plannerRegistry(t)
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(first): %v", err)
	}
	materializePlanFixture(t, root, first)
	lock := first.Lock
	for i := range lock.Modules {
		if lock.Modules[i].ID == "ggg/element/button" {
			lock.Modules[i].Migrations = []LockedMigration{{
				ID: "retained", Number: 3, Path: "internal/web/templates/ui/card.templ", SHA256: testDigestA,
			}}
		}
	}
	lockData, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock: %v", err)
	}
	writeTestFile(t, root, "gogogadget.lock.json", lockData)
	_, err = engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil || !strings.Contains(err.Error(), "collides with authored target") {
		t.Fatalf("Plan error = %v, want retained migration collision rejection", err)
	}
}

func TestEnginePlanRejectsRetainedMigrationReservedTarget(t *testing.T) {
	registry := plannerRegistry(t)
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/component/card"}, Exclude: []string{},
	})
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: registry}}})
	first, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(first): %v", err)
	}
	materializePlanFixture(t, root, first)
	lock := first.Lock
	lock.Modules[0].Migrations = []LockedMigration{{
		ID: "retained-reserved", Number: 3, Path: "gogogadget.lock.json", SHA256: testDigestA,
	}}
	lockData, err := MarshalLock(lock)
	if err != nil {
		t.Fatalf("MarshalLock: %v", err)
	}
	writeTestFile(t, root, "gogogadget.lock.json", lockData)
	_, err = engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("Plan error = %v, want reserved target rejection", err)
	}
}

func TestEnginePlanRejectsUnsupportedAndCancelledOperations(t *testing.T) {
	engine := New(Options{Source: staticSource{snapshot: Snapshot{Commit: testCommitA, FS: plannerRegistry(t)}}})
	if _, err := engine.Plan(context.Background(), t.TempDir(), Operation{Kind: OpInit}); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("Plan(OpInit) error = %v, want unsupported operation", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Plan(ctx, t.TempDir(), Operation{Kind: OpSync}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan(cancelled) error = %v, want context cancellation", err)
	}
}

func TestRewriteModuleImports(t *testing.T) {
	const canonical = "github.com/gogogadget/gogogadget"
	const target = "example.com/acme/app"

	t.Run("Go import specs only", func(t *testing.T) {
		input := []byte(`package sample

import (
	canonical "github.com/gogogadget/gogogadget"
	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/gogogadget/gogogadgetx/keep"
)

const keep = "github.com/gogogadget/gogogadget/internal/modkit"
// github.com/gogogadget/gogogadget/internal/modkit
var _ = canonical.OpSync
`)
		got, err := rewriteModuleImports("sample.go", input, canonical, target)
		if err != nil {
			t.Fatalf("rewriteModuleImports: %v", err)
		}
		text := string(got)
		if !strings.Contains(text, `canonical "example.com/acme/app"`) ||
			!strings.Contains(text, `"example.com/acme/app/internal/modkit"`) {
			t.Fatalf("imports not rewritten:\n%s", text)
		}
		if !strings.Contains(text, `"github.com/gogogadget/gogogadgetx/keep"`) ||
			!strings.Contains(text, `const keep = "github.com/gogogadget/gogogadget/internal/modkit"`) {
			t.Fatalf("non-import text changed:\n%s", text)
		}
	})

	t.Run("templ import block only", func(t *testing.T) {
		input := []byte(`package sample

import "github.com/gogogadget/gogogadget/internal/modkit"

templ Sample() {
	<div data-value="github.com/gogogadget/gogogadget/internal/modkit"></div>
}
`)
		got, err := rewriteModuleImports("sample.templ", input, canonical, target)
		if err != nil {
			t.Fatalf("rewriteModuleImports: %v", err)
		}
		text := string(got)
		if !strings.Contains(text, `import "example.com/acme/app/internal/modkit"`) {
			t.Fatalf("templ import not rewritten:\n%s", text)
		}
		if !strings.Contains(text, `data-value="github.com/gogogadget/gogogadget/internal/modkit"`) {
			t.Fatalf("templ body changed:\n%s", text)
		}
	})

	t.Run("malformed import", func(t *testing.T) {
		_, err := rewriteModuleImports("broken.go", []byte("package broken\nimport (\n\"unterminated\n"), canonical, target)
		if err == nil {
			t.Fatal("rewriteModuleImports accepted malformed import")
		}
	})
}
