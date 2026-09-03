package modkit

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exampleTestRoot is the repository root as seen from this package's directory.
func exampleTestRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..")
}

func loadExampleCatalog(t *testing.T) Catalog {
	t.Helper()
	catalog, err := LoadCatalog(os.DirFS(filepath.Join(exampleTestRoot(t), ExampleRegistryDir)))
	if err != nil {
		t.Fatalf("LoadCatalog(%s): %v", ExampleRegistryDir, err)
	}
	return catalog
}

// The validator regenerates the sqlc output package for a closure that
// installs a query file, and restores it by path after removal. A sqlc.yaml
// that stopped naming that path would make the restore a silent no-op, the
// generated package would leak past removal, and the byte-for-byte claim would
// stop being about it. So the duplicated path must fail loudly instead.
func TestExampleValidatorRefusesAMovedSQLCOutput(t *testing.T) {
	if err := assertSQLCOutputDir(exampleTestRoot(t)); err != nil {
		t.Fatalf("this repository's %s should satisfy the check: %v", sqlcConfigFile, err)
	}

	moved := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(moved, sqlcConfigFile), []byte(
		"version: \"2\"\nsql:\n  - queries: \"internal/db/queries\"\n"+
			"    gen:\n      go:\n        out: \"internal/db/generated\"\n"), 0o644))
	err := assertSQLCOutputDir(moved)
	require.Error(t, err)
	assert.Contains(t, err.Error(), sqlcOutputDir)

	renamedQueries := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(renamedQueries, sqlcConfigFile), []byte(
		"version: \"2\"\nsql:\n  - queries: \"internal/db/sql\"\n"+
			"    gen:\n      go:\n        out: \"internal/db/sqlc\"\n"), 0o644))
	err = assertSQLCOutputDir(renamedQueries)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal/db/queries")

	missing := t.TempDir()
	require.Error(t, assertSQLCOutputDir(missing))
}

func exampleIDs(closures []exampleClosure) map[string]struct{} {
	out := map[string]struct{}{}
	for _, closure := range closures {
		for _, module := range closure.modules {
			out[module.ID] = struct{}{}
		}
	}
	return out
}

// copyExampleRegistry copies the example registry into a temporary root so a
// test can break a manifest without touching the repository.
func copyExampleRegistry(t *testing.T) string {
	t.Helper()
	source := filepath.Join(exampleTestRoot(t), ExampleRegistryDir)
	destination := t.TempDir()
	if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	}); err != nil {
		t.Fatalf("copy example registry: %v", err)
	}
	return destination
}

func mutateExampleManifest(t *testing.T, registry, id string, mutate func(*Manifest)) {
	t.Helper()
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		t.Fatalf("invalid module id %q", id)
	}
	kind, name, ok := parts[1], parts[2], true
	if !ok {
		t.Fatalf("invalid module id %q", id)
	}
	path := filepath.Join(registry, "registry", "modules", kind, name, "module.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var document ModuleDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	mutate(&document.Module)
	encoded, err := marshalIndented(document)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// planAgainstExampleRegistry plans one example closure into an empty project.
// Planning writes nothing, so an empty project is enough to reach every refusal
// that does not need a compiler.
func planAgainstExampleRegistry(t *testing.T, registry, id string) (Plan, error) {
	t.Helper()
	root := writeTargetProject(t, "example.test/derivative", Project{
		Schema:     2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{id},
		Exclude: []string{},
	})
	engine := New(Options{Source: DirectorySource{Root: registry}, Generator: RegistryGenerator{}})
	return engine.Plan(context.Background(), root, Operation{Kind: OpSync, Offline: true})
}

// A lock released only by a deferred cleanup is permanent the moment a run is
// killed - Ctrl-C, a CI timeout, an OOM - and the command then refuses forever
// with no process left to wait for. That turns a concurrency guard into
// something a human has to repair by deleting a temp file they have no reason to
// know exists. So the lock records its owner and a later run reclaims it when
// that owner is gone.
func TestValidateLockIsReclaimedWhenItsOwnerIsGone(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "validate.lock")

	// A pid that cannot be running. Reclaimed rather than refused.
	require.NoError(t, os.WriteFile(lockPath, []byte("999999\n"), 0o644))
	require.NoError(t, acquireValidateLock(lockPath))

	owner, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), strings.TrimSpace(string(owner)),
		"the reclaiming run must record itself as the new owner, or the next run cannot tell who holds it")
}

// The concurrency guarantee still has to hold: two runs share one work directory
// keyed on the project path, and the second one's cleanup would delete the
// first's tree mid-build. That surfaces as files vanishing under a live run and
// reads like a harness bug, so the second run is refused outright.
func TestValidateLockRefusesALiveOwner(t *testing.T) {
	holder := exec.Command("sleep", "60")
	require.NoError(t, holder.Start())
	t.Cleanup(func() {
		_ = holder.Process.Kill()
		_, _ = holder.Process.Wait()
	})

	lockPath := filepath.Join(t.TempDir(), "validate.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte(strconv.Itoa(holder.Process.Pid)+"\n"), 0o644))

	err := acquireValidateLock(lockPath)
	require.Error(t, err, "a live owner must be refused, or two runs corrupt one work directory")
	assert.Contains(t, err.Error(), strconv.Itoa(holder.Process.Pid),
		"the refusal must name the pid so the caller can check it themselves rather than guess")
}

// An unreadable or malformed lock names no process, so refusing on its behalf
// would block forever for nothing.
func TestValidateLockTreatsAnUnreadableLockAsStale(t *testing.T) {
	for name, content := range map[string]string{
		"empty":        "",
		"not a number": "whatever\n",
		"negative":     "-1\n",
	} {
		lockPath := filepath.Join(t.TempDir(), "validate.lock")
		require.NoError(t, os.WriteFile(lockPath, []byte(content), 0o644))
		assert.NoErrorf(t, acquireValidateLock(lockPath), "%s lock must be reclaimed", name)
	}
}

// The pid must be in the file before the file exists under lockPath. With
// O_EXCL followed by a separate write, a second run landing in that window reads
// an empty file, validateLockOwner classifies it as malformed-and-therefore-
// stale, and it deletes a live run's lock — the exact reclaim path
// TestValidateLockTreatsAnUnreadableLockAsStale relies on, turned against a live
// owner. Two runs then share one work directory, which is the corruption the
// lock exists to prevent.
//
// A watcher polling the path while it is published and removed in a tight loop
// is what makes that window observable. The probe is deliberately one-
// directional: a correct publish can NEVER be caught, so this cannot flake into
// a failure. It can in principle miss a violation, which is why the invariant is
// also enforced structurally below.
func TestValidateLockIsNeverVisibleWithoutItsOwner(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "validate.lock")
	stop := make(chan struct{})
	caught := make(chan struct{}, 1)
	watching := make(chan struct{})

	go func() {
		close(watching)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if info, err := os.Stat(lockPath); err == nil && info.Size() == 0 {
				caught <- struct{}{}
				return
			}
		}
	}()
	<-watching

	for range 3000 {
		require.NoError(t, publishValidateLock(lockPath))
		require.NoError(t, os.Remove(lockPath))
	}
	close(stop)

	select {
	case <-caught:
		t.Fatal("the lock became visible with no owner recorded; a concurrent run reads that as stale and steals a live lock")
	default:
	}
}

// A published lock always names its owner, and publishing over a live one is
// refused without touching the incumbent's bytes — link, not rename.
func TestValidateLockPublishIsExclusiveAndNamesItsOwner(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "validate.lock")
	require.NoError(t, publishValidateLock(lockPath))

	owner, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(os.Getpid()), strings.TrimSpace(string(owner)),
		"the lock existed without naming its owner")

	err = publishValidateLock(lockPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrExist)
	after, readErr := os.ReadFile(lockPath)
	require.NoError(t, readErr)
	assert.Equal(t, string(owner), string(after), "a refused publish rewrote the live lock")

	// No staged temp file survives in the lock directory.
	entries, err := os.ReadDir(filepath.Dir(lockPath))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.Equal(t, "validate.lock", entry.Name(), "publishValidateLock leaked a staged file")
	}
}

// A lock naming THIS pid is stale by construction: one process never runs two
// validations concurrently, so it is debris from an earlier run in this process.
// Waiting for ourselves would deadlock the command outright.
func TestValidateLockTreatsOurOwnPidAsStale(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "validate.lock")
	require.NoError(t, os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644))
	assert.NoError(t, acquireValidateLock(lockPath))
}

// The flag vocabulary, and the one place it is allowed to be lenient. A bare
// `ggg registry validate` still means every family, because that is the
// question an operator asks; anything else must be a family this build knows,
// or the run would silently narrow to nothing.
func TestClosureFamilyParsing(t *testing.T) {
	for value, want := range map[string]ClosureFamily{
		"":         ClosureFamilyAll,
		"all":      ClosureFamilyAll,
		"core":     ClosureFamilyCore,
		"external": ClosureFamilyExternal,
	} {
		family, err := ParseClosureFamily(value)
		require.NoErrorf(t, err, "closure family %q", value)
		assert.Equal(t, want, family)
	}
	for _, value := range []string{"CORE", "example", "core,external", "none"} {
		_, err := ParseClosureFamily(value)
		assert.Errorf(t, err, "closure family %q must be refused, not narrowed to nothing", value)
	}
}

// A named family that covers nothing is a refusal, and that is what keeps a
// CI job pinned to one family from passing green after its fixtures are gone.
// The all family keeps the accommodating answer, because a derivative that
// vendored the fixtures away still has to be able to run the command.
func TestNamedClosureFamilyRefusesWhenItCoversNothing(t *testing.T) {
	empty := t.TempDir()

	for _, family := range selectableClosureFamilies {
		_, err := closuresForFamily(empty, family, io.Discard)
		require.Errorf(t, err, "family %s exercised nothing and reported success", family)
		assert.Contains(t, err.Error(), string(family))
	}

	closures, err := closuresForFamily(empty, ClosureFamilyAll, io.Discard)
	require.NoError(t, err, "a project with no fixtures must still be able to run the command")
	assert.Empty(t, closures)
}

// Two families never share a work directory. They are two CI jobs and two
// operator invocations, and a shared derivative would mean one run's cleanup
// deleting the other's tree mid-build — or, with the pid lock, the second run
// refused outright.
func TestClosureFamiliesGetSeparateWorkDirectories(t *testing.T) {
	seen := map[string]ClosureFamily{}
	for _, family := range append(slices.Clone(selectableClosureFamilies), ClosureFamilyAll) {
		dir := exampleWorkDir("/projects/app", family)
		if other, clash := seen[dir]; clash {
			t.Fatalf("families %s and %s share the work directory %s", other, family, dir)
		}
		seen[dir] = family
	}
	assert.NotEqual(t,
		exampleWorkDir("/projects/app", ClosureFamilyCore),
		exampleWorkDir("/projects/other", ClosureFamilyCore),
		"two checkouts must not share one derivative")
}
