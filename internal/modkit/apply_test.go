package modkit

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
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

func (g *scriptedGenerator) Generate(_ context.Context, plan Plan) error {
	root := plan.Root
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

func (g *scriptedGenerator) GeneratedPaths(_ Plan) []string {
	paths := make([]string, 0, len(g.writes))
	for path := range g.writes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func (g *scriptedGenerator) Render(_ context.Context, _ Plan) ([]GeneratedFile, error) {
	files := make([]GeneratedFile, 0, len(g.writes))
	for path, content := range g.writes {
		files = append(files, GeneratedFile{Path: path, Content: string(content)})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, g.err
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
		Schema: 2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules:  []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
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
	mutatePlannerModule(t, changed, "ggg/page/optional", func(module *Manifest) {
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

// scenarioRegistryPath is the aggregate emitScenarioRegistry stops rendering
// once no selected module declares a scenario. It carries no `_registry_gen.`
// infix, so the old stale detector could not even see it.
const scenarioRegistryPath = "internal/web/templates/scenarios_gen.go"

// scenarioRemovalProject installs two modules where only page/optional declares
// a dev scenario, so removing it empties that emitter's input set entirely.
func scenarioRemovalProject(t *testing.T) (string, *Engine) {
	t.Helper()
	first, _ := removalRegistries(t)
	mutatePlannerModule(t, first, "ggg/page/optional", func(module *Manifest) {
		module.Runtime.Scenarios = []ScenarioContribution{{
			Slug: "optional", Title: "Optional", Summary: "Fixture scenario.",
			Layout: "app", Surfaces: []string{"card"}, States: []string{"default"},
		}}
	})
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules:  []string{"ggg/component/card", "ggg/page/optional"}, Exclude: []string{},
	})
	return root, New(Options{Source: source, Generator: RegistryGenerator{}})
}

// A removal that empties an emitter's input set must delete the aggregate that
// emitter stops rendering. Left behind, it still compiles into the project and
// still references renderers the removal deleted, and `sync --check` reports it
// forever with an instruction — "run ggg sync" — that could not clear it.
func TestApplyDeletesAggregatesTheGraphNoLongerOwns(t *testing.T) {
	root, engine := scenarioRemovalProject(t)
	ctx := context.Background()

	install, err := engine.Plan(ctx, root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(install): %v", err)
	}
	if _, err := engine.Apply(ctx, install); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(scenarioRegistryPath))); err != nil {
		t.Fatalf("install did not render %s: %v", scenarioRegistryPath, err)
	}

	remove, err := engine.Plan(ctx, root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove): %v", err)
	}
	if _, err := engine.Apply(ctx, remove); err != nil {
		t.Fatalf("Apply(remove): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(scenarioRegistryPath))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stale aggregate %s survived the removal: %v", scenarioRegistryPath, err)
	}

	// And the gate agrees: nothing is left for `sync --check` to complain about.
	check, err := engine.Plan(ctx, root, Operation{Kind: OpSync, DryRun: true})
	if err != nil {
		t.Fatalf("Plan(check): %v", err)
	}
	drift, err := generatedDriftDiagnostics(ctx, engine, check)
	if err != nil {
		t.Fatalf("generatedDriftDiagnostics: %v", err)
	}
	if len(drift) != 0 {
		t.Fatalf("generated drift after removal: %#v", drift)
	}
}

// A rollback must put the deleted aggregate back: a delete the journal did not
// snapshot would survive as a missing file that nothing re-renders.
func TestApplyRestoresDeletedAggregateOnFailure(t *testing.T) {
	root, engine := scenarioRemovalProject(t)
	ctx := context.Background()

	install, err := engine.Plan(ctx, root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(install): %v", err)
	}
	if _, err := engine.Apply(ctx, install); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	before, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(scenarioRegistryPath)))
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}

	remove, err := engine.Plan(ctx, root, Operation{Kind: OpRemove, Modules: []string{"ggg/page/optional"}})
	if err != nil {
		t.Fatalf("Plan(remove): %v", err)
	}
	// Same plan, but the generation stage sweeps and then fails.
	sweepThenFail := &sweepingGenerator{inner: RegistryGenerator{}, err: errors.New("templ: boom")}
	result, err := applyEngineWith(sweepThenFail).Apply(ctx, remove)
	if err == nil {
		t.Fatal("Apply returned nil on generator failure")
	}
	if !result.RolledBack {
		t.Fatalf("result = %#v, want a completed rollback", result)
	}
	after, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(scenarioRegistryPath)))
	if err != nil {
		t.Fatalf("deleted aggregate not restored: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Fatalf("aggregate bytes not restored:\nbefore %q\nafter %q", before, after)
	}
}

// sweepingGenerator performs the real render-and-sweep and then fails, which is
// how a templ or Tailwind failure looks from Apply's side.
type sweepingGenerator struct {
	inner RegistryGenerator
	err   error
}

func (g *sweepingGenerator) Render(ctx context.Context, plan Plan) ([]GeneratedFile, error) {
	return g.inner.Render(ctx, plan)
}

func (g *sweepingGenerator) GeneratedPaths(plan Plan) []string { return g.inner.GeneratedPaths(plan) }

func (g *sweepingGenerator) Generate(ctx context.Context, plan Plan) error {
	if err := g.inner.Generate(ctx, plan); err != nil {
		return err
	}
	return g.err
}

// Every installed and generated file must be group- and world-readable.
// os.CreateTemp opens at 0600 and nothing chmods it, so without an explicit
// mode every file `ggg add` writes is owner-only — which breaks a group
// checkout and any container that builds as one uid and runs as another.
func TestApplyWritesGroupReadableFiles(t *testing.T) {
	root, engine := scenarioRemovalProject(t)
	install, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(install): %v", err)
	}
	if _, err := engine.Apply(context.Background(), install); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	for _, path := range []string{
		"internal/modules/optional.go", // authored
		scenarioRegistryPath,           // generated
		"gogogadget.lock.json",         // lock
	} {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != defaultFileMode {
			t.Errorf("%s mode = %04o, want %04o", path, got, defaultFileMode)
		}
	}
}

// Rollback must restore the mode it found, not a hardcoded one. A file the
// operator made executable and a file they made 0600 must both come back the
// way they were.
func TestApplyRollbackRestoresOriginalMode(t *testing.T) {
	root, engine, initial := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), initial); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}
	authored := filepath.Join(root, "internal/modules/optional.go")
	const custom fs.FileMode = 0o640
	if err := os.Chmod(authored, custom); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	changed, _ := removalRegistries(t)
	newOptional := []byte("package optional\n\nconst Version = 99\n")
	changed["registry/modules/page/optional/optional.go"] = &fstest.MapFile{Data: newOptional}
	mutatePlannerModule(t, changed, "ggg/page/optional", func(module *Manifest) {
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
	info, err := os.Stat(authored)
	if err != nil {
		t.Fatalf("stat after rollback: %v", err)
	}
	if got := info.Mode().Perm(); got != custom {
		t.Fatalf("mode after rollback = %04o, want %04o", got, custom)
	}
	_ = engine
}

// A rollback that cannot restore a path must say so, and must not report
// RolledBack: true. Exit 5's one job is telling the operator whether the tree is
// trustworthy, and disk-full is just as likely to defeat the restore as it was
// to cause the generation failure in the first place.
func TestApplyReportsIncompleteRollback(t *testing.T) {
	root, _, initial := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{}).Apply(context.Background(), initial); err != nil {
		t.Fatalf("Apply(install): %v", err)
	}

	changed, _ := removalRegistries(t)
	newOptional := []byte("package optional\n\nconst Version = 99\n")
	changed["registry/modules/page/optional/optional.go"] = &fstest.MapFile{Data: newOptional}
	mutatePlannerModule(t, changed, "ggg/page/optional", func(module *Manifest) {
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

	// Make the restore fail the way a revoked write or a full disk would: the
	// generation stage strips write permission from the very file the rollback
	// has to put back.
	authored := filepath.Join(root, "internal", "modules", "optional.go")
	failGen := &unwritableTargetGenerator{path: authored, err: errors.New("templ: boom")}
	t.Cleanup(func() { _ = os.Chmod(authored, 0o644) })

	result, err := applyEngineWith(failGen).Apply(context.Background(), update)
	if err == nil {
		t.Fatal("Apply returned nil on generator failure")
	}
	if result.RolledBack {
		t.Fatal("RolledBack is true after a restore that could not complete")
	}
	// A partial restore is the MORE severe outcome, so it must not downgrade out
	// of exit 5 — that is the one exit code telling the operator to look at the
	// tree by hand. The CLI keys on Exit, not RolledBack, for exactly this.
	if result.Exit != 5 {
		t.Fatalf("Exit = %d after an incomplete rollback, want 5", result.Exit)
	}
	if !strings.Contains(err.Error(), "rollback incomplete") ||
		!strings.Contains(err.Error(), "internal/modules/optional.go") {
		t.Fatalf("error does not name the unrestorable path: %v", err)
	}
}

// unwritableTargetGenerator revokes write permission on one path and then fails,
// so the rollback that follows cannot restore that path's bytes.
type unwritableTargetGenerator struct {
	path string
	err  error
}

func (g *unwritableTargetGenerator) Render(context.Context, Plan) ([]GeneratedFile, error) {
	return nil, nil
}
func (g *unwritableTargetGenerator) GeneratedPaths(Plan) []string { return nil }
func (g *unwritableTargetGenerator) Generate(context.Context, Plan) error {
	if err := os.Chmod(g.path, 0o400); err != nil {
		return err
	}
	return g.err
}

// Rollback must also remove the directories MkdirAll created, or an install
// that failed leaves an empty package directory behind.
func TestApplyRollbackRemovesCreatedDirectories(t *testing.T) {
	root, engine, plan := installTwoModules(t)
	if _, err := applyEngineWith(&scriptedGenerator{err: errors.New("templ: boom")}).
		Apply(context.Background(), plan); err == nil {
		t.Fatal("Apply returned nil on generator failure")
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "modules")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("directory created by the failed apply survived rollback: %v", err)
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
