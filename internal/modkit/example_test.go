package modkit

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
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

// The example manifests are hand-authored, and `ggg registry build` deliberately
// does not touch them: it scans registry/modules, which is the shipped catalog,
// not this separate registry. So editing a payload without updating its digest
// would leave a manifest that only fails later, inside a derivative, as a
// "payload sha256 mismatch" with no hint of the right value. This is that hint.
func TestExampleRegistryDigestsMatchPayloads(t *testing.T) {
	root := filepath.Join(exampleTestRoot(t), ExampleRegistryDir)
	for _, module := range loadExampleCatalog(t).Modules {
		for _, file := range module.Files {
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Source)))
			if err != nil {
				t.Fatalf("%s: read payload %s: %v", module.ID, file.Source, err)
			}
			if got := digestBytes(content); got != file.SHA256 {
				t.Errorf("%s: payload %s digest is %s; update the manifest sha256 (recorded %s)",
					module.ID, file.Source, got, file.SHA256)
			}
		}
		for _, migration := range module.Migrations {
			content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(migration.Source)))
			if err != nil {
				t.Fatalf("%s: read migration %s: %v", module.ID, migration.Source, err)
			}
			if got := digestBytes(content); got != migration.SHA256 {
				t.Errorf("%s: migration %s digest is %s; update the manifest sha256 (recorded %s)",
					module.ID, migration.Source, got, migration.SHA256)
			}
		}
	}
}

// The examples are installable by design, which is the whole point of them and
// also the only thing that could make them dangerous. Their isolation is
// structural rather than a flag: no shipped index names them, so this project's
// catalog cannot resolve one, no profile can list one, the committed lock has
// never installed one — and because every generated wiring file is rendered from
// that lock, Boot cannot reach them either. If any of that stops being true this
// fails here rather than in production.
func TestExampleModulesAreUnreachableFromTheShippedCatalog(t *testing.T) {
	root := exampleTestRoot(t)
	if err := assertExamplesUnreachable(root, loadExampleCatalog(t)); err != nil {
		t.Fatalf("example isolation broken: %v", err)
	}
}

// One example per kind, or the lifecycle is only proved for the kinds that
// happen to be present. A closure must also carry its example dependencies
// ahead of the module that requires them, because that ordering is what the
// planner installs.
func TestExampleClosuresCoverEveryKindInDependencyOrder(t *testing.T) {
	closures, err := exampleClosures(loadExampleCatalog(t))
	if err != nil {
		t.Fatalf("exampleClosures: %v", err)
	}
	kinds := make([]string, 0, len(closures))
	for _, closure := range closures {
		kinds = append(kinds, string(closure.root.Kind))
		if closure.modules[len(closure.modules)-1].ID != closure.root.ID {
			t.Fatalf("closure %s does not end at its own root: %v", closure.root.ID, closure.ids())
		}
		installed := make([]string, 0, len(closure.modules))
		for _, module := range closure.modules {
			for _, required := range module.Requires {
				if _, isExample := exampleIDs(closures)[required.ID]; !isExample {
					continue
				}
				if !slices.Contains(installed, required.ID) {
					t.Fatalf("closure %s installs %s before its dependency %s",
						closure.root.ID, module.ID, required.ID)
				}
			}
			installed = append(installed, module.ID)
		}
	}
	want := []string{"element", "component", "page", "workflow", "system"}
	if !slices.Equal(kinds, want) {
		t.Fatalf("closure kinds = %v, want exactly %v", kinds, want)
	}
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

// A broken example must be refused, not installed. These two cases are cheap
// because the planner answers them without building anything: an undeclared
// dependency is a graph failure and a generated target is a preflight refusal.
// The third failure the validator catches — a manifest that forgets to declare a
// file its own source needs — is only visible to a compiler, so it is proved by
// the command itself rather than here.
//
// Both cases break the leaf element, because it is the one example whose
// requires are empty: any other module would first fail on a shipped dependency
// the standalone example registry does not contain, and the test would pass for
// the wrong reason.
func TestExampleWithMissingDependencyIsRefused(t *testing.T) {
	registry := copyExampleRegistry(t)
	mutateExampleManifest(t, registry, "ggg/element/example-token", func(m *Manifest) {
		m.Requires = append(m.Requires, Requirement{ID: "ggg/element/example-missing", Contract: ContractBounds{Min: 1, Max: 1}})
		slices.SortFunc(m.Requires, func(a, b Requirement) int { return strings.Compare(a.ID, b.ID) })
	})

	_, err := planAgainstExampleRegistry(t, registry, "ggg/element/example-token")
	if err == nil {
		t.Fatal("planning a closure with an undeclared dependency succeeded")
	}
	const want = `module "ggg/element/example-token" requires missing dependency "ggg/element/example-missing"`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}

func TestExampleClaimingGeneratedOutputIsRefused(t *testing.T) {
	registry := copyExampleRegistry(t)
	mutateExampleManifest(t, registry, "ggg/element/example-token", func(m *Manifest) {
		for i := range m.Files {
			if m.Files[i].Class == FileClassGo {
				m.Files[i].Target = "internal/web/templates/ui/example_token_templ.go"
			}
		}
	})

	_, err := planAgainstExampleRegistry(t, registry, "ggg/element/example-token")
	if err == nil {
		t.Fatal("planning a module that authors a generated output succeeded")
	}
	const want = "module ggg/element/example-token targets generated output " +
		"internal/web/templates/ui/example_token_templ.go; " +
		"generated outputs are tool-owned and cannot be authored"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
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
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
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
