package modkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func migrationModule(t *testing.T, id string, count int) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	first, _ := removalRegistries(t)
	second := cloneMapFS(first)
	var migrations []ManifestMigration
	for i := range count {
		migID := id
		if count > 1 {
			migID = fmt.Sprintf("%s-%c", id, 'a'+i)
		}
		source := "registry/modules/page/optional/migrations/" + migID + ".sql"
		content := []byte("-- " + migID + "\nSELECT 1;\n")
		first[source] = &fstest.MapFile{Data: content}
		second[source] = &fstest.MapFile{Data: content}
		migrations = append(migrations, ManifestMigration{
			ID: migID, Kind: MigrationImmutable, Source: source, SHA256: sha256Hex(content),
		})
	}
	mutatePlannerModule(t, first, "page/optional", func(m *Manifest) { m.Migrations = migrations })
	mutatePlannerModule(t, second, "page/optional", func(m *Manifest) { m.Migrations = migrations })
	return first, second
}

// Adopted on-disk migrations 0001..0019 set the next-free global number to 20.
func TestNewMigrationStartsAfterAdoptedBaseline(t *testing.T) {
	first, _ := migrationModule(t, "opt-forward", 1)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:   1,
		Registry: ProjectRegistry{Repository: "local/registry", Ref: "main"},
		Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
	})
	// Adopted immutable ledger: 0001..0019 already present on disk.
	for i := 1; i <= 19; i++ {
		name := fmt.Sprintf("internal/db/migrations/%04d_adopted.sql", i)
		writeTestFile(t, root, name, []byte("-- adopted\n"))
	}

	plan, err := New(Options{Source: source}).Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	change := plannedChange(t, plan, "internal/db/migrations/0020_opt_forward.sql")
	if change.Class != DestinationMigration {
		t.Fatalf("allocated migration class = %q, want migration", change.Class)
	}
	for _, c := range plan.Changes {
		if strings.Contains(c.Path, "_adopted.sql") && c.Kind != ChangeUnchanged {
			t.Fatalf("adopted migration touched: %#v", c)
		}
	}
}

// A second sync after applying the first must be a byte no-op.
func TestSecondSyncIsByteNoOp(t *testing.T) {
	root, engine, first := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), first); err != nil {
		t.Fatalf("Apply(first): %v", err)
	}
	second, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(second): %v", err)
	}
	for _, change := range second.Changes {
		if change.Kind != ChangeUnchanged {
			t.Fatalf("second sync not a no-op: %#v", change)
		}
	}
	if len(second.Conflicts) != 0 {
		t.Fatalf("second sync reported conflicts: %#v", second.Conflicts)
	}
	// Applying the second plan must not rewrite the lock bytes.
	lockBefore, err := os.ReadFile(filepath.Join(root, "gogogadget.lock.json"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), second); err != nil {
		t.Fatalf("Apply(second): %v", err)
	}
	lockAfter, err := os.ReadFile(filepath.Join(root, "gogogadget.lock.json"))
	if err != nil {
		t.Fatalf("read lock after: %v", err)
	}
	if !slices.Equal(lockBefore, lockAfter) {
		t.Fatal("second sync rewrote the lock")
	}
}

// An allocated migration must never be rewritten on a later sync/update.
func TestImmutableMigrationNeverRewritten(t *testing.T) {
	first, second := migrationModule(t, "opt-forward", 1)
	source := refSource{snapshots: map[string]Snapshot{
		"v1":        {Commit: testCommitA, FS: first},
		"v2":        {Commit: testCommitB, FS: second},
		testCommitB: {Commit: testCommitB, FS: second},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:   1,
		Registry: ProjectRegistry{Repository: "local/registry", Ref: "v1"},
		Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	materializeConflictPlan(t, root, initial)
	migPath := "internal/db/migrations/0001_opt_forward.sql"
	if _, err := os.Stat(filepath.Join(root, migPath)); err != nil {
		t.Fatalf("migration missing: %v", err)
	}

	update, err := engine.Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "v2"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	for _, change := range update.Changes {
		if change.Path == migPath && change.Kind != ChangeUnchanged {
			t.Fatalf("update rewrote immutable migration: %#v", change)
		}
	}
	for _, module := range update.Lock.Modules {
		if module.ID == "page/optional" {
			if len(module.Migrations) != 1 || module.Migrations[0].Number != 1 {
				t.Fatalf("migration mapping changed: %#v", module.Migrations)
			}
		}
	}
}

// Authored module targets must never claim tool-owned generated outputs.
func TestAuthoredTargetCannotClaimGeneratedOutputs(t *testing.T) {
	first, _ := removalRegistries(t)
	mutatePlannerModule(t, first, "page/optional", func(m *Manifest) {
		m.Files = append(m.Files, ManifestFile{
			Source: "registry/modules/page/optional/gen.templ",
			Target: "static/app.css",
			Class:  FileClassStyle, SHA256: sha256Hex([]byte("body{}")),
		})
	})
	first["registry/modules/page/optional/gen.templ"] = &fstest.MapFile{Data: []byte("body{}")}
	source := refSource{snapshots: map[string]Snapshot{
		"main": {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:   1,
		Registry: ProjectRegistry{Repository: "local/registry", Ref: "main"},
		Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
	})
	_, err := New(Options{Source: source}).Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil || !strings.Contains(err.Error(), "generated") {
		t.Fatalf("Plan error = %v, want refusal for tool-owned generated output", err)
	}
}
