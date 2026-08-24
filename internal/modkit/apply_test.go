package modkit

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"testing/fstest"
)

// scriptedGenerator lets tests prove that Apply runs the fixed pipeline once,
// writes the lock only on success, and rolls back on failure.
type scriptedGenerator struct {
	err    error
	calls  int
	writes map[string][]byte
}

func (g *scriptedGenerator) Generate(_ context.Context, root string) error {
	g.calls++
	for path, content := range g.writes {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return err
		}
	}
	return g.err
}

func (g *scriptedGenerator) GeneratedPaths(_ string) []string {
	paths := make([]string, 0, len(g.writes))
	for path := range g.writes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func applyEngineWith(gen Generator) *Engine {
	return New(Options{Generator: gen})
}
func installTwoModules(t *testing.T) (string, *Engine, Plan) {
	t.Helper()
	first, _ := removalRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema:   1,
		Registry: ProjectRegistry{Repository: "local/registry", Ref: "main"},
		Modules:  []string{"component/card", "page/optional"}, Exclude: []string{},
	})
	engine := New(Options{Source: source})
	initial, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(initial): %v", err)
	}
	return root, engine, initial
}

func TestApplyWritesAuthoredChangesAndLock(t *testing.T) {
	root, engine, plan := installTwoModules(t)
	gen := &scriptedGenerator{}
	result, err := applyEngineWith(gen).Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Exit != 0 || result.RolledBack {
		t.Fatalf("result = exit %d rolledback %t, want clean", result.Exit, result.RolledBack)
	}
	if gen.calls != 1 {
		t.Fatalf("generator ran %d times, want exactly 1", gen.calls)
	}
	for _, path := range []string{"internal/modules/button.go", "internal/modules/optional.go", "gogogadget.json", "gogogadget.lock.json"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("expected %s on disk: %v", path, err)
		}
	}
	lockData, err := os.ReadFile(filepath.Join(root, "gogogadget.lock.json"))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	gotLock, err := ParseLock(lockData)
	if err != nil {
		t.Fatalf("ParseLock(applied): %v", err)
	}
	if gotLock.RegistryCommit != plan.Lock.RegistryCommit || len(gotLock.Modules) != len(plan.Lock.Modules) {
		t.Fatalf("applied lock diverged: commit %q modules %d", gotLock.RegistryCommit, len(gotLock.Modules))
	}
	_ = engine
}

func TestApplyDryRunPerformsNoWrites(t *testing.T) {
	root, engine, plan := installTwoModules(t)
	plan.Operation.DryRun = true
	before := snapshotTree(t, root)
	result, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("Apply(dry-run): %v", err)
	}
	if result.Exit != 0 {
		t.Fatalf("dry-run exit = %d, want 0", result.Exit)
	}
	if !reflectDeepEqualTrees(before, snapshotTree(t, root)) {
		t.Fatalf("dry-run mutated the tree (before %d entries)", len(before))
	}
	_ = engine
}

func TestApplyRollsBackOnGeneratorFailure(t *testing.T) {
	root, engine, plan := installTwoModules(t)
	beforeIntent, err := os.ReadFile(filepath.Join(root, "gogogadget.json"))
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}
	result, err := applyEngineWith(&scriptedGenerator{err: errors.New("templ: boom")}).Apply(context.Background(), plan)
	if err == nil {
		t.Fatal("Apply returned nil error on generator failure")
	}
	if result.Exit != 5 || !result.RolledBack {
		t.Fatalf("result = exit %d rolledback %t, want rollback", result.Exit, result.RolledBack)
	}
	if _, err := os.Stat(filepath.Join(root, "internal/modules/button.go")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("authored file survived rollback: %v", err)
	}
	afterIntent, err := os.ReadFile(filepath.Join(root, "gogogadget.json"))
	if err != nil {
		t.Fatalf("read intent after rollback: %v", err)
	}
	if !slices.Equal(beforeIntent, afterIntent) {
		t.Fatal("intent bytes changed across rollback")
	}
	if _, err := os.Stat(filepath.Join(root, "gogogadget.lock.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("lock survived rollback: %v", err)
	}
	_ = engine
}

func TestApplyRestoresPreRunAuthoredBytesOnFailure(t *testing.T) {
	root, engine, initial := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), initial); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	original, err := os.ReadFile(filepath.Join(root, "internal/modules/optional.go"))
	if err != nil {
		t.Fatalf("read authored: %v", err)
	}

	// A follow-up sync rewrites optional.go with new upstream bytes, then
	// generation fails and Apply must restore the pre-run authored content.
	changed, _ := removalRegistries(t)
	newOptional := []byte("package optional\n\nconst Version = 99\n")
	changed["registry/modules/page/optional/optional.go"] = &fstest.MapFile{Data: newOptional}
	mutatePlannerModule(t, changed, "page/optional", func(module *Manifest) {
		module.Revision = 2
		module.Files[0].SHA256 = sha256Hex(newOptional)
	})
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitB, FS: changed},
		testCommitB: {Commit: testCommitB, FS: changed},
	}}
	update, err := New(Options{Source: source}).Plan(context.Background(), root, Operation{Kind: OpUpdate, RegistryRef: "main"})
	if err != nil {
		t.Fatalf("Plan(update): %v", err)
	}
	if _, err := applyEngineWith(&scriptedGenerator{err: errors.New("tailwind: boom")}).Apply(context.Background(), update); err == nil {
		t.Fatal("Apply(update) returned nil on generator failure")
	}
	after, err := os.ReadFile(filepath.Join(root, "internal/modules/optional.go"))
	if err != nil {
		t.Fatalf("read authored after rollback: %v", err)
	}
	if !slices.Equal(original, after) {
		t.Fatalf("authored bytes not restored:\nbefore %q\nafter %q", original, after)
	}
	_ = engine
}

func TestApplyRestoresGeneratorOwnedOutputsOnFailure(t *testing.T) {
	root, engine, initial := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), initial); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	genPath := "internal/modules/registry_gen.go"
	preRun := []byte("// pre-run generated content\n")
	writeTestFile(t, root, genPath, preRun)

	// Generation writes its own output then fails; Apply must restore the
	// generator-owned path to its exact pre-run bytes.
	failGen := &scriptedGenerator{
		err:    errors.New("templ: boom"),
		writes: map[string][]byte{genPath: []byte("// dirty generated content\n")},
	}
	_, err := applyEngineWith(failGen).Apply(context.Background(), initial)
	if err == nil {
		t.Fatal("Apply returned nil on generator failure")
	}
	after, err := os.ReadFile(filepath.Join(root, genPath))
	if err != nil {
		t.Fatalf("read generated after rollback: %v", err)
	}
	if !slices.Equal(preRun, after) {
		t.Fatalf("generator-owned bytes not restored:\nbefore %q\nafter %q", preRun, after)
	}
	_ = engine
}

func reflectDeepEqualTrees(before, after map[string]string) bool {
	if len(before) != len(after) {
		return false
	}
	for path, digest := range before {
		if after[path] != digest {
			return false
		}
	}
	return true
}
