package modkit

// The example-module lifecycle proof behind `ggg registry validate`.
//
// A source registry only works if a third party can author a module, install
// it, compile it, and take it back out. Every other check in this package is a
// statement about data: manifests parse, digests match, namespaces do not
// collide. None of them prove the round trip, because none of them ever build
// anything. This does: it installs each example closure that registry/testdata
// publishes into a throwaway copy of this repository, runs generation, compiles
// it, runs the module's own tests, removes it, and then asserts the tree came
// back byte for byte — with exactly one class of exception, the immutable
// migration ledger, which must survive because a database that has already run
// a migration cannot be told to un-run it.
//
// The examples deliberately do NOT carry test_only. That flag is refused by
// every production lock (validateLockedModule) and by every production index
// (LoadCatalog), which is exactly right for a fixture that must never be
// installed — and exactly wrong for a fixture whose whole purpose is to BE
// installed. Their isolation is structural instead of a flag, which is
// stronger: they live in a separate registry root under registry/testdata that
// no shipped index references, so this repository's catalog cannot resolve
// them, no profile can name them, nothing can reach them from
// gogogadget.json, and therefore nothing generated from the lock can reach
// Boot. assertExamplesUnreachable enforces that on every run.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// ExampleRegistryDir is the self-contained registry root, relative to the
// project root, that publishes the example modules. It is a complete registry
// of its own — its own registry.json and its own kind indexes — rather than
// extra items in the shipped indexes, because that is what makes the examples
// unreachable from this project rather than merely flagged.
const ExampleRegistryDir = "registry/testdata"

// ExampleResult is what one exercised closure proved. Every field is an
// observation, not a claim: paths that were installed, generated, compiled,
// tested, retained after removal, and the count of tree entries compared.
type ExampleResult struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Modules   []string `json:"modules"`
	Installed []string `json:"installed"`
	Generated []string `json:"generated"`
	Compiled  []string `json:"compiled"`
	Tested    []string `json:"tested"`
	// ProviderSlot and ProviderSelections are populated for the mail/storage
	// permutation closures. They make the environment switch an observed part
	// of registry validation rather than a claim hidden in fixture metadata.
	ProviderSlot       string                       `json:"provider_slot,omitempty"`
	ProviderSelections map[string]ProviderSelection `json:"provider_selections,omitempty"`
	ProviderSwitched   bool                         `json:"provider_switched,omitempty"`
	Retained           []string                     `json:"retained"`
	LockIdentityOnly   []string                     `json:"lock_identity_only"`
	Compared           int                          `json:"compared"`
}

type providerFixtureSpec struct {
	slot, local, managed string
	legacy               []string
}

func providerFixtureSpecFor(id string) (providerFixtureSpec, bool) {
	switch id {
	case "fixture/system/mail-providers":
		return providerFixtureSpec{
			slot: "ggg/mail", local: "fixture/system/mail-local", managed: "fixture/system/mail-managed",
			legacy: []string{"ggg/system/mail-dev", "ggg/system/mail-resend"},
		}, true
	case "fixture/system/storage-providers":
		return providerFixtureSpec{
			slot: "ggg/storage", local: "fixture/system/storage-local", managed: "fixture/system/storage-managed",
			legacy: []string{"ggg/system/storage-filesystem", "ggg/system/storage-s3"},
		}, true
	default:
		return providerFixtureSpec{}, false
	}
}

// exampleClosure is one example module plus the example modules it pulls in.
// Production dependencies are not listed: the derivative already has the whole
// shipped catalog installed, so a closure names only what publishing this
// example adds.
type exampleClosure struct {
	root    Manifest
	modules []Manifest
}

func (c exampleClosure) ids() []string {
	out := make([]string, 0, len(c.modules))
	for _, m := range c.modules {
		out = append(out, m.ID)
	}
	return out
}

// ValidateExamples exercises every example closure end to end and returns one
// result per closure. It writes progress to log so a long run is legible.
//
// A project without an example registry gets no results and no error. The
// examples ship with the upstream catalog, and a derivative that vendored only
// the modules it selected has nothing to exercise — refusing there would make
// the command unusable in exactly the projects the registry exists to serve.
// That this repository still has one is asserted by
// TestExampleRegistryDigestsMatchPayloads, so the skip cannot quietly become
// the answer here.
//
// The derivative lives at a path derived from the project root rather than a
// fresh mkdtemp: Go's build cache keys on the compiling directory, so a stable
// path turns the second and every later closure into a warm build. Two
// concurrent runs against one repository would fight over it, which is why this
// is a command an operator invokes and not something a Make target runs.
func ValidateExamples(ctx context.Context, root string, log io.Writer) ([]ExampleResult, error) {
	canonicalRoot, err := canonicalProjectRoot(root)
	if err != nil {
		return nil, err
	}
	examples := filepath.Join(canonicalRoot, ExampleRegistryDir)
	if _, statErr := os.Stat(filepath.Join(examples, "registry.json")); errors.Is(statErr, fs.ErrNotExist) {
		fmt.Fprintf(log, "no example registry at %s; nothing to exercise\n", ExampleRegistryDir)
		return nil, nil
	} else if statErr != nil {
		return nil, statErr
	}
	catalog, err := LoadCatalog(os.DirFS(examples))
	if err != nil {
		return nil, fmt.Errorf("load example registry %s: %w", ExampleRegistryDir, err)
	}
	closures, err := exampleClosures(catalog)
	if err != nil {
		return nil, err
	}
	if len(closures) == 0 {
		return nil, fmt.Errorf("%s publishes no example modules", ExampleRegistryDir)
	}
	if err := assertExamplesUnreachable(canonicalRoot, catalog); err != nil {
		return nil, err
	}

	work := exampleWorkDir(canonicalRoot)
	template := filepath.Join(work, "template")
	derivative := filepath.Join(work, "derivative")
	// The work path is stable per project so Go's directory-keyed build cache
	// stays warm, which is what keeps this command at seconds per closure rather
	// than a full rebuild each time. The cost is that two concurrent runs would
	// share it, and the second one's cleanup would delete the first one's tree
	// mid-build — which surfaces as a file vanishing under a live run and reads
	// like a harness bug. So the second run is refused outright instead.
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", work, err)
	}
	lockPath := filepath.Join(work, "validate.lock")
	if err := acquireValidateLock(lockPath); err != nil {
		return nil, err
	}
	for _, path := range []string{template, derivative} {
		if err := os.RemoveAll(path); err != nil {
			return nil, fmt.Errorf("clear %s: %w", path, err)
		}
	}
	keep := false
	defer func() {
		_ = os.Remove(lockPath)
		if keep {
			fmt.Fprintf(log, "derivative kept for inspection at %s\n", derivative)
			return
		}
		_ = os.RemoveAll(work)
	}()

	fmt.Fprintf(log, "preparing derivative from %s\n", canonicalRoot)
	if err := prepareTemplateDerivative(ctx, canonicalRoot, template, log); err != nil {
		keep = true
		return nil, err
	}

	results := make([]ExampleResult, 0, len(closures))
	for _, closure := range closures {
		result, err := exerciseExampleClosure(ctx, canonicalRoot, template, derivative, closure, log)
		if err != nil {
			keep = true
			return results, fmt.Errorf("%s: %w", closure.root.ID, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// acquireValidateLock refuses a second concurrent run and reclaims a lock whose
// owner is gone. The lock records the pid, because a lock that only a deferred
// cleanup can release is permanent the moment a run is killed - Ctrl-C, a CI
// timeout, an OOM - and the command then refuses forever with no process to
// wait for. That turns a concurrency guard into a machine that has to be
// repaired by hand, and the first person to hit it has no reason to suspect a
// temp file.
//
// Liveness is checked with signal 0, which reports whether the pid can be
// signalled without delivering anything. A pid that has been recycled by a
// different process is the residual risk; it is bounded by the work directory
// being project-scoped and the window being one run, and the alternative -
// refusing until a human intervenes - is the failure this exists to remove.
func acquireValidateLock(lockPath string) error {
	for attempt := range 2 {
		err := publishValidateLock(lockPath)
		if err == nil {
			return nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return err
		}
		if attempt == 1 {
			return fmt.Errorf(
				"another registry validate is already running against this project; "+
					"wait for it to finish, or delete %s if no run is live", lockPath)
		}
		owner, alive := validateLockOwner(lockPath)
		if alive {
			return fmt.Errorf(
				"registry validate is already running against this project as pid %d; "+
					"wait for it to finish, or delete %s if that process is gone", owner, lockPath)
		}
		// The owner is gone, so the lock is debris from an interrupted run.
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear stale lock %s: %w", lockPath, err)
		}
	}
	return nil
}

// publishValidateLock creates lockPath containing this run's pid, atomically.
// The pid must be in the file before the file exists under that name: with
// O_EXCL followed by a separate write, a second run landing in that window reads
// an empty file, validateLockOwner classifies it as malformed and therefore
// stale, and it deletes a live run's lock. So the content is written to a temp
// file and hard-linked into place — link, not rename, because rename would
// silently clobber an existing lock instead of reporting fs.ErrExist.
func publishValidateLock(lockPath string) error {
	staged, err := os.CreateTemp(filepath.Dir(lockPath), ".validate-lock-*")
	if err != nil {
		return err
	}
	stagedName := staged.Name()
	defer func() { _ = os.Remove(stagedName) }()
	if _, err := fmt.Fprintf(staged, "%d\n", os.Getpid()); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	return os.Link(stagedName, lockPath)
}

// validateLockOwner reads the recorded pid and reports whether it is still
// running. An unreadable or malformed lock is treated as stale: it cannot name a
// process to wait for, so refusing on its behalf would block for nothing.
//
// A lock recording THIS pid is also treated as stale. One process never runs two
// validations concurrently, so seeing our own pid means an earlier run in this
// process left the file behind - and waiting for ourselves would deadlock the
// command outright.
func validateLockOwner(lockPath string) (int, bool) {
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return 0, false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	return pid, process.Signal(syscall.Signal(0)) == nil
}

// exampleWorkDir is the stable per-repository scratch path. The digest keeps two
// checkouts of this repository from sharing one derivative.
func exampleWorkDir(root string) string {
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(os.TempDir(), "ggg-registry-validate-"+hex.EncodeToString(sum[:8]))
}

// exampleClosures groups the published examples into installable closures, one
// per module, ordered element → component → page → workflow → system so the
// output reads from leaf to provider. A closure carries its example
// dependencies in topological order, because installing the root without them
// is not a thing the planner will do.
func exampleClosures(catalog Catalog) ([]exampleClosure, error) {
	byID := make(map[string]Manifest, len(catalog.Modules))
	for _, m := range catalog.Modules {
		byID[m.ID] = m
	}
	var closures []exampleClosure
	for _, kind := range moduleKindOrder {
		for _, m := range catalog.Modules {
			if m.Kind != kind {
				continue
			}
			if strings.HasPrefix(m.ID, "fixture/") {
				if _, providerFixture := providerFixtureSpecFor(m.ID); !providerFixture {
					continue
				}
			}
			ordered, err := exampleClosureOrder(m, byID, nil)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", m.ID, err)
			}
			closures = append(closures, exampleClosure{root: m, modules: ordered})
		}
	}
	return closures, nil
}

// exampleClosureOrder returns the example modules to install for one root, with
// every dependency before the module that requires it. Requirements outside the
// example registry are skipped: they are shipped modules the derivative already
// has.
func exampleClosureOrder(m Manifest, byID map[string]Manifest, seen []string) ([]Manifest, error) {
	if slices.Contains(seen, m.ID) {
		return nil, fmt.Errorf("example dependency cycle includes %q", m.ID)
	}
	seen = append(seen, m.ID)
	var out []Manifest
	for _, requirement := range m.Requires {
		dependency, isExample := byID[requirement.ID]
		if !isExample {
			continue
		}
		nested, err := exampleClosureOrder(dependency, byID, seen)
		if err != nil {
			return nil, err
		}
		for _, candidate := range nested {
			if !slices.ContainsFunc(out, func(existing Manifest) bool { return existing.ID == candidate.ID }) {
				out = append(out, candidate)
			}
		}
	}
	return append(out, m), nil
}

// assertExamplesUnreachable is the isolation guard. The examples are installable
// by design, so the only thing standing between a fixture and production is that
// nothing shipped can name one. That is checked here rather than assumed from
// the fact that nobody added them: the shipped catalog must not contain any
// example id, no profile may list one, and the committed lock must not have
// installed one — which is what makes it impossible for Boot to reach them,
// since every generated wiring file is rendered from that lock.
func assertExamplesUnreachable(root string, examples Catalog) error {
	ids := make(map[string]struct{}, len(examples.Modules))
	for _, m := range examples.Modules {
		ids[m.ID] = struct{}{}
	}

	production, err := LoadCatalog(os.DirFS(root))
	if err != nil {
		return fmt.Errorf("load shipped catalog: %w", err)
	}
	for _, m := range production.Modules {
		if _, isExample := ids[m.ID]; isExample {
			return fmt.Errorf(
				"example module %s is listed in a shipped registry index; examples must stay unreachable from %s",
				m.ID, ProjectFileName)
		}
	}
	for _, profile := range production.Profiles {
		for _, member := range profile.Members {
			if _, isExample := ids[member]; isExample {
				return fmt.Errorf("profile %s lists example module %s", profile.ID, member)
			}
		}
	}

	lockData, err := os.ReadFile(filepath.Join(root, LockFileName))
	if err != nil {
		return fmt.Errorf("read %s: %w", LockFileName, err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		return err
	}
	for _, module := range lock.Modules {
		if _, isExample := ids[module.ID]; isExample {
			return fmt.Errorf("committed lock installs example module %s", module.ID)
		}
	}
	return nil
}

// prepareTemplateDerivative copies the working tree, pins the project's module
// selection explicitly, and syncs once so the copy is a settled derivative
// before any example touches it.
//
// The selection is pinned because `ggg remove` records an exclusion whenever any
// profile is still selected, whether or not the removed module was ever a
// profile member. Against `profile/full` that would leave an exclusion behind
// for an example that no profile ever named, and the byte-for-byte claim would
// have to carve out gogogadget.json. Pinning the resolved list is a legitimate
// derivative shape (a fork that freezes its module set) and it makes removal the
// exact inverse of installation.
func prepareTemplateDerivative(ctx context.Context, root, template string, log io.Writer) error {
	if err := copyProjectTree(root, template); err != nil {
		return err
	}

	// The derivative copies the working tree, not the last commit, so it can
	// inherit a payload whose manifest digest has not been rebuilt yet — which
	// is a normal state mid-edit and is exactly what `ggg sync --check` exists
	// to report. Refusing here would make the example lifecycle proof hostage to
	// somebody else's unrelated in-flight edit, so the derivative rebuilds its
	// own digests and says how many it touched. This never writes to root.
	refreshed, err := refreshManifestDigests(template)
	if err != nil {
		return fmt.Errorf("rebuild derivative manifest digests: %w", err)
	}
	if len(refreshed) != 0 {
		fmt.Fprintf(log, "  rebuilt %d manifest digest(s) in the derivative (stale upstream, reported by sync --check)\n",
			len(refreshed))
	}

	engine := New(Options{Source: DirectorySource{Root: template}, Generator: RegistryGenerator{}})
	snapshot, resolveErr := engine.source.Resolve(ctx, "", "")
	if resolveErr != nil {
		return fmt.Errorf("resolve derivative registry: %w", resolveErr)
	}
	catalog, err := LoadCatalog(snapshot.FS)
	if err != nil {
		return fmt.Errorf("load derivative catalog: %w", err)
	}
	projectData, err := os.ReadFile(filepath.Join(template, ProjectFileName))
	if err != nil {
		return fmt.Errorf("read derivative %s: %w", ProjectFileName, err)
	}
	project, err := ParseProject(projectData)
	if err != nil {
		return err
	}
	graph, err := resolveSelectedGraph(ctx, project, catalog)
	if err != nil {
		return err
	}
	pinned := project
	pinned.Modules = append([]string{}, graph.order...)
	sort.Strings(pinned.Modules)
	pinned.Exclude = []string{}
	pinnedData, err := MarshalProject(pinned)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(template, ProjectFileName), pinnedData, 0o644); err != nil {
		return fmt.Errorf("write derivative %s: %w", ProjectFileName, err)
	}

	if _, err := syncDerivative(ctx, template); err != nil {
		return fmt.Errorf("settle derivative: %w", err)
	}
	return nil
}

// syncDerivative plans and applies one sync against a derivative, returning the
// applied plan.
func syncDerivative(ctx context.Context, derivative string) (Plan, error) {
	return applyDerivativeOperation(ctx, derivative, Operation{Kind: OpSync, Offline: true})
}

func applyDerivativeOperation(ctx context.Context, derivative string, op Operation) (Plan, error) {
	engine := New(Options{Source: DirectorySource{Root: derivative}, Generator: RegistryGenerator{}})
	plan, err := engine.Plan(ctx, derivative, op)
	if err != nil {
		return Plan{}, err
	}
	if len(plan.Conflicts) != 0 {
		return plan, fmt.Errorf("%s left %d conflict(s) staged", op.Kind, len(plan.Conflicts))
	}
	result, err := engine.Apply(ctx, plan)
	if err != nil {
		return plan, err
	}
	if result.Exit != exitOK || result.RolledBack {
		return plan, fmt.Errorf("%s applied with exit %d (rolled back %t)", op.Kind, result.Exit, result.RolledBack)
	}
	return plan, nil
}

// exerciseExampleClosure runs the whole lifecycle for one closure against a
// pristine copy of the settled derivative.
func exerciseExampleClosure(
	ctx context.Context, root, template, derivative string, closure exampleClosure, log io.Writer,
) (ExampleResult, error) {
	if spec, ok := providerFixtureSpecFor(closure.root.ID); ok {
		return exerciseProviderClosure(ctx, root, template, derivative, closure, spec, log)
	}
	return exerciseStandardClosure(ctx, root, template, derivative, closure, log)
}

func providerFixtureChoices(spec providerFixtureSpec) ProviderSelections {
	return ProviderSelections{
		Development: ProviderSelection{Adapter: spec.local, Target: "filesystem"},
		Test:        ProviderSelection{Adapter: spec.local, Target: "filesystem"},
		Production:  ProviderSelection{Adapter: spec.managed, Target: "managed"},
	}
}

func copyProviderSelections(values map[string]ProviderSelections) map[string]ProviderSelections {
	out := make(map[string]ProviderSelections, len(values))
	for slot, choices := range values {
		out[slot] = choices
	}
	return out
}

func providerLegacyChoices(spec providerFixtureSpec) ProviderSelections {
	choices := ProviderSelections{}
	switch spec.slot {
	case "ggg/mail":
		choices.Development = ProviderSelection{Adapter: "ggg/system/mail-dev", Target: "filesystem"}
		choices.Test = ProviderSelection{Adapter: "ggg/system/mail-dev", Target: "filesystem"}
		choices.Production = ProviderSelection{Adapter: "ggg/system/mail-resend", Target: "resend"}
	case "ggg/storage":
		choices.Development = ProviderSelection{Adapter: "ggg/system/storage-filesystem", Target: "filesystem"}
		choices.Test = ProviderSelection{Adapter: "ggg/system/storage-filesystem", Target: "filesystem"}
		choices.Production = ProviderSelection{Adapter: "ggg/system/storage-s3", Target: "r2"}
	}
	return choices
}

func switchProviderSelections(root, slot string, choices ProviderSelections) error {
	data, err := os.ReadFile(filepath.Join(root, ProjectFileName))
	if err != nil {
		return err
	}
	project, err := ParseProject(data)
	if err != nil {
		return err
	}
	project.Providers = copyProviderSelections(project.Providers)
	project.Providers[slot] = choices
	updated, err := MarshalProject(project)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ProjectFileName), updated, 0o644)
}

func exerciseProviderClosure(
	ctx context.Context, root, template, derivative string, closure exampleClosure,
	spec providerFixtureSpec, log io.Writer,
) (ExampleResult, error) {
	result, err := exerciseStandardClosure(ctx, root, template, derivative, closure, log)
	if err != nil {
		return result, err
	}
	result.ProviderSlot = spec.slot
	result.ProviderSelections = map[string]ProviderSelection{
		"development": {Adapter: spec.local, Target: "filesystem"},
		"test":        {Adapter: spec.local, Target: "filesystem"},
		"production":  {Adapter: spec.managed, Target: "managed"},
	}
	result.ProviderSwitched = true
	return result, nil
}

func exerciseStandardClosure(
	ctx context.Context, root, template, derivative string, closure exampleClosure, log io.Writer,
) (ExampleResult, error) {

	ids := closure.ids()
	result := ExampleResult{ID: closure.root.ID, Kind: string(closure.root.Kind), Modules: ids}
	fmt.Fprintf(log, "\n%s\n  closure: %s\n", closure.root.ID, strings.Join(ids, ", "))

	if err := os.RemoveAll(derivative); err != nil {
		return result, fmt.Errorf("clear derivative: %w", err)
	}
	if err := copyProjectTree(template, derivative); err != nil {
		return result, err
	}
	before, err := hashProjectTree(derivative)
	if err != nil {
		return result, err
	}
	baselineGenerated, err := readGeneratedOutputs(derivative)
	if err != nil {
		return result, err
	}
	baselineLock, err := readDerivativeLock(derivative)
	if err != nil {
		return result, err
	}
	var baselineProject []byte
	if _, isProviderFixture := providerFixtureSpecFor(closure.root.ID); isProviderFixture {
		baselineProject, err = os.ReadFile(filepath.Join(derivative, ProjectFileName))
		if err != nil {
			return result, err
		}
	}
	if spec, isProviderFixture := providerFixtureSpecFor(closure.root.ID); isProviderFixture {
		if err := switchProviderSelections(derivative, spec.slot, providerFixtureChoices(spec)); err != nil {
			return result, fmt.Errorf("select fixture providers: %w", err)
		}
		if _, err := applyDerivativeOperation(ctx, derivative,
			Operation{Kind: OpRemove, Modules: append([]string{}, spec.legacy...), Offline: true}); err != nil {
			return result, fmt.Errorf("remove existing %s adapters: %w", spec.slot, err)
		}
	}

	if err := publishExamples(root, derivative, closure.modules); err != nil {
		return result, err
	}
	plan, err := applyDerivativeOperation(ctx, derivative, Operation{Kind: OpAdd, Modules: ids, Offline: true})
	if err != nil {
		return result, fmt.Errorf("install: %w", err)
	}
	for _, change := range plan.Changes {
		switch change.Class {
		case DestinationAuthored, DestinationMigration:
			if change.Kind != ChangeUnchanged {
				result.Installed = append(result.Installed, change.Path)
			}
		}
	}
	sort.Strings(result.Installed)
	fmt.Fprintf(log, "  installed %d file(s)\n", len(result.Installed))

	installedLock, err := readDerivativeLock(derivative)
	if err != nil {
		return result, err
	}
	for _, path := range exampleGeneratedDelta(before, derivative) {
		result.Generated = append(result.Generated, path)
	}

	// templ output is not distributed, so it has to be produced here or the
	// installed renderer does not exist as Go. One file per invocation: a batch
	// can emit nothing for one input and still exit 0.
	for _, path := range result.Installed {
		if !strings.HasSuffix(path, ".templ") {
			continue
		}
		if err := runGoTool(ctx, derivative, "tool", "templ", "generate", "-f", path); err != nil {
			return result, err
		}
		result.Generated = append(result.Generated, strings.TrimSuffix(path, ".templ")+"_templ.go")
	}
	sort.Strings(result.Generated)
	result.Generated = dedupeSorted(result.Generated)

	if err := runGoTool(ctx, derivative, "build", "./..."); err != nil {
		return result, fmt.Errorf("compile: %w", err)
	}
	result.Compiled = []string{"./..."}
	fmt.Fprintf(log, "  compiled ./... and generated %d file(s)\n", len(result.Generated))

	// Only the closure's own declared tests, not the derivative's whole suite.
	// That is a real limitation and worth naming: the derivative's suite would
	// fail on TestGalleryCoversEveryInstalledComponent, because the generated
	// component registry gains the installed component while the gallery that
	// must render it is a hand-written templ owned by page/dev-gallery with no
	// extension point. Verified by running it: "installed components the gallery
	// never renders [example-callout (feedback)]". That is a gap in the gallery
	// contract for third-party UI modules, not something this closure can fix,
	// so it is reported rather than silently worked around.
	packages := exampleTestPackages(closure.modules)
	if len(packages) != 0 {
		args := append([]string{"test", "-count=1", "-run", "^TestExample"}, packages...)
		if err := runGoTool(ctx, derivative, args...); err != nil {
			return result, fmt.Errorf("module tests: %w", err)
		}
		result.Tested = packages
		fmt.Fprintf(log, "  module tests passed in %s\n", strings.Join(packages, ", "))
	}

	// A second sync must not move any authored or generated byte. The lock is
	// exempt and cannot be otherwise: the registry is this tree, so installing a
	// file that the registry publishes from a different path changes the
	// resolved snapshot commit, and the lock records that commit.
	settle, err := syncDerivative(ctx, derivative)
	if err != nil {
		return result, fmt.Errorf("re-sync: %w", err)
	}
	for _, change := range settle.Changes {
		if change.Kind == ChangeUnchanged || change.Class == DestinationLock {
			continue
		}
		return result, fmt.Errorf("a second sync rewrote %s (%s); installation is not idempotent",
			change.Path, change.Kind)
	}

	if spec, isProviderFixture := providerFixtureSpecFor(closure.root.ID); isProviderFixture {
		if err := switchProviderSelections(derivative, spec.slot, providerLegacyChoices(spec)); err != nil {
			return result, fmt.Errorf("restore provider selections: %w", err)
		}
	}
	if _, err := applyDerivativeOperation(ctx, derivative,
		Operation{Kind: OpRemove, Modules: ids, Offline: true}); err != nil {
		return result, fmt.Errorf("remove: %w", err)
	}
	if err := unpublishExamples(derivative, closure.modules); err != nil {
		return result, err
	}
	// One settling sync after the module is gone, which is what an operator does
	// (`ggg remove` then `make generate`). Without it the aggregates would still
	// carry the commit resolved while the example was installed — a
	// self-hosting artifact rather than anything about the module. Nothing
	// authored may move here.
	if spec, isProviderFixture := providerFixtureSpecFor(closure.root.ID); isProviderFixture {
		if err := restoreLegacyPayloads(template, derivative, baselineLock, spec.legacy); err != nil {
			return result, fmt.Errorf("restore %s adapter payloads: %w", spec.slot, err)
		}
	}
	if baselineProject != nil {
		if err := os.WriteFile(filepath.Join(derivative, ProjectFileName), baselineProject, 0o644); err != nil {
			return result, fmt.Errorf("restore project selections: %w", err)
		}
	}
	settled, err := syncDerivative(ctx, derivative)
	if err != nil {
		return result, fmt.Errorf("settle after removal: %w", err)
	}
	for _, change := range settled.Changes {
		if change.Kind == ChangeUnchanged || change.Class == DestinationLock ||
			change.Class == DestinationGenerated {
			continue
		}
		return result, fmt.Errorf("the settling sync rewrote %s (%s, %s) after removal",
			change.Path, change.Kind, change.Class)
	}

	retained := retainedMigrations(installedLock, ids)
	result.Retained = make([]string, 0, len(retained))
	for _, migration := range retained {
		result.Retained = append(result.Retained, migration.Path)
	}
	headers, err := assertTreeRestored(derivative, before, baselineGenerated, retained)
	if err != nil {
		return result, err
	}
	result.LockIdentityOnly = headers
	result.Compared = len(before)
	if err := assertLockRestored(baselineLock, derivative, ids, retained); err != nil {
		return result, err
	}
	fmt.Fprintf(log,
		"  removed; %d tree entries restored, %d aggregate(s) differ only in the lock-identity header, %d migration(s) retained\n",
		result.Compared, len(result.LockIdentityOnly), len(result.Retained))
	return result, nil
}
func restoreLegacyPayloads(template, derivative string, baseline Lock, ids []string) error {
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	for _, locked := range baseline.Modules {
		if _, ok := wanted[locked.ID]; !ok {
			continue
		}
		for _, file := range locked.Manifest.Files {
			source := filepath.Join(template, filepath.FromSlash(file.Source))
			target := filepath.Join(derivative, filepath.FromSlash(file.Target))
			data, err := os.ReadFile(source)
			if err != nil {
				return fmt.Errorf("read %s: %w", file.Source, err)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// exampleGeneratedDelta names the registry aggregates the install rewrote. It is
// derived by comparing digests rather than asked of the generator, so it reports
// what actually changed rather than what the pipeline owns.
func exampleGeneratedDelta(before map[string]string, derivative string) []string {
	after, err := hashProjectTree(derivative)
	if err != nil {
		return nil
	}
	var changed []string
	for path, digest := range after {
		if !IsGeneratedOutputPath(path) {
			continue
		}
		if previous, existed := before[path]; !existed || previous != digest {
			changed = append(changed, path)
		}
	}
	sort.Strings(changed)
	return changed
}

// exampleTestPackages is the deduplicated set of Go packages the closure's
// modules declare tests in.
func exampleTestPackages(modules []Manifest) []string {
	var packages []string
	for _, m := range modules {
		for _, pkg := range m.Tests.GoPackages {
			candidate := "./" + strings.TrimPrefix(pkg, "./")
			if !slices.Contains(packages, candidate) {
				packages = append(packages, candidate)
			}
		}
	}
	sort.Strings(packages)
	return packages
}

// retainedMigrations is every immutable migration the closure allocated. These
// are the only paths allowed to survive removal.
func retainedMigrations(lock Lock, ids []string) []LockedMigration {
	var out []LockedMigration
	for _, module := range lock.Modules {
		if !slices.Contains(ids, module.ID) {
			continue
		}
		out = append(out, module.Migrations...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// publishExamples vendors the example manifests into the derivative's own
// catalog, which is what a fork does when it adopts a third-party module: the
// manifest joins the fork's indexes, and its payload paths are re-pointed at
// wherever the fork put the payload — here the vendored registry/testdata tree
// the copy already carries. The bytes are untouched, so every declared digest
// still verifies; only the path the planner reads them from moves.
func publishExamples(root, derivative string, modules []Manifest) error {
	for _, m := range modules {
		item, err := catalogItemPath(m)
		if err != nil {
			return err
		}
		source := filepath.Join(root, ExampleRegistryDir, filepath.FromSlash(item))
		raw, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("read example manifest %s: %w", item, err)
		}
		var published ModuleDocument
		if err := decodeStrict(raw, &published); err != nil {
			return fmt.Errorf("decode example manifest %s: %w", item, err)
		}
		for i := range published.Module.Files {
			published.Module.Files[i].Source = ExampleRegistryDir + "/" + published.Module.Files[i].Source
		}
		for i := range published.Module.Migrations {
			published.Module.Migrations[i].Source = ExampleRegistryDir + "/" + published.Module.Migrations[i].Source
		}
		document, err := marshalIndented(published)
		if err != nil {
			return fmt.Errorf("encode vendored manifest %s: %w", item, err)
		}
		target := filepath.Join(derivative, filepath.FromSlash(item))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(item), err)
		}
		if err := os.WriteFile(target, document, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", item, err)
		}
		if err := rewriteCatalogIndex(derivative, m.Kind, func(items []string) []string {
			if slices.Contains(items, item) {
				return items
			}
			items = append(items, item)
			sort.Strings(items)
			return items
		}); err != nil {
			return err
		}
	}
	return nil
}

// unpublishExamples reverses publishExamples. The publication is the harness's
// own act, not something removal is claimed to undo, so it is reversed here
// before the tree is compared.
func unpublishExamples(derivative string, modules []Manifest) error {
	for _, m := range modules {
		item, err := catalogItemPath(m)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(derivative, filepath.Dir(filepath.FromSlash(item)))); err != nil {
			return fmt.Errorf("remove vendored %s: %w", item, err)
		}
		if err := rewriteCatalogIndex(derivative, m.Kind, func(items []string) []string {
			return slices.DeleteFunc(items, func(candidate string) bool { return candidate == item })
		}); err != nil {
			return err
		}
	}
	return nil
}

func catalogItemPath(m Manifest) (string, error) {
	_, kind, name, scoped := splitScopedModuleID(m.ID)
	if scoped {
		if !validModuleKind(ModuleKind(kind)) {
			return "", fmt.Errorf("module id %q is invalid", m.ID)
		}
		return "registry/modules/" + kind + "/" + name + "/module.json", nil
	}
	if err := validateInstallableModuleID(m.ID); err != nil {
		return "", err
	}
	return "registry/modules/" + string(m.Kind) + "/" + m.Name + "/module.json", nil
}

// rewriteCatalogIndex edits one kind index in place, preserving the exact
// encoding the shipped indexes use so publishing and un-publishing are byte
// inverses of each other.
func rewriteCatalogIndex(derivative string, kind ModuleKind, mutate func([]string) []string) error {
	path := ""
	for _, include := range catalogIncludes {
		if string(include.kind) == string(kind) {
			path = include.path
		}
	}
	if path == "" {
		return fmt.Errorf("no catalog index for kind %q", kind)
	}
	full := filepath.Join(derivative, filepath.FromSlash(path))
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	var index CatalogIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	index.Items = mutate(index.Items)
	encoded, err := marshalIndented(index)
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(full, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// assertTreeRestored is the byte-for-byte claim, and it returns the one
// tolerated class of difference rather than hiding it.
//
// Every entry that existed before the install must exist afterwards. Authored
// files must be byte-identical, full stop. A generated aggregate may differ in
// exactly its lock-identity header lines — `index:` is a digest of the resolved
// graph and `registry:` is the resolved commit, and the lock genuinely still
// remembers the removal as a tombstone, so a header that came back identical
// would mean the lock had forgotten. Its BODY must be byte-identical: a
// leftover route, translation, component entry or environment key would show up
// there, which is the leak this check exists to catch. The only new entries
// permitted are the retained migrations, at their allocated digests.
func assertTreeRestored(
	derivative string, before map[string]string, baselineGenerated map[string][]byte, retained []LockedMigration,
) ([]string, error) {
	after, err := hashProjectTree(derivative)
	if err != nil {
		return nil, err
	}
	allowedExtra := make(map[string]string, len(retained))
	for _, migration := range retained {
		allowedExtra[migration.Path] = migration.SHA256
	}

	var missing, changed, leaked, headerOnly []string
	for path, digest := range before {
		current, present := after[path]
		switch {
		case !present:
			missing = append(missing, path)
		case current == digest, path == LockFileName:
		case IsGeneratedOutputPath(path):
			identical, err := generatedBodyUnchanged(derivative, path, baselineGenerated[path])
			if err != nil {
				return nil, err
			}
			if !identical {
				changed = append(changed, path)
				continue
			}
			headerOnly = append(headerOnly, path)
		default:
			changed = append(changed, path)
		}
	}
	for path := range after {
		if _, existed := before[path]; existed {
			continue
		}
		expected, allowed := allowedExtra[path]
		if !allowed {
			leaked = append(leaked, path)
			continue
		}
		if after[path] != expected {
			return nil, fmt.Errorf("retained migration %s has digest %s, want the allocated %s",
				path, after[path], expected)
		}
		delete(allowedExtra, path)
	}
	sort.Strings(missing)
	sort.Strings(changed)
	sort.Strings(leaked)
	sort.Strings(headerOnly)
	switch {
	case len(missing) != 0:
		return nil, fmt.Errorf("removal deleted %d file(s) it does not own: %s", len(missing), strings.Join(missing, ", "))
	case len(changed) != 0:
		return nil, fmt.Errorf("removal left %d file(s) with different content: %s", len(changed), strings.Join(changed, ", "))
	case len(leaked) != 0:
		return nil, fmt.Errorf("removal left %d file(s) behind: %s", len(leaked), strings.Join(leaked, ", "))
	}
	for path := range allowedExtra {
		return nil, fmt.Errorf("migration %s was allocated but is not on disk after removal", path)
	}
	return headerOnly, nil
}

// lockIdentityHeader matches the two header lines a generated aggregate stamps
// its lock identity into, in each of the three comment syntaxes the emitters
// use. It is anchored and length-exact so it cannot swallow a body line that
// happens to mention a digest.
var lockIdentityHeader = regexp.MustCompile(`(?m)^(//|#|/\*) (index|registry): [0-9a-f]{64}( \*/)?$`)

// generatedBodyUnchanged reports whether a generated file differs from its
// baseline only in the lock-identity header.
func generatedBodyUnchanged(derivative, path string, baseline []byte) (bool, error) {
	if baseline == nil {
		return false, nil
	}
	current, err := os.ReadFile(filepath.Join(derivative, filepath.FromSlash(path)))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	const placeholder = "$1 $2: <lock identity>$3"
	return bytes.Equal(
		lockIdentityHeader.ReplaceAll(baseline, []byte(placeholder)),
		lockIdentityHeader.ReplaceAll(current, []byte(placeholder)),
	), nil
}

// readGeneratedOutputs captures the bytes of every generated aggregate, which is
// what the header-tolerant comparison needs and digests alone cannot give.
func readGeneratedOutputs(root string) (map[string][]byte, error) {
	digests, err := hashProjectTree(root)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, 64)
	for path := range digests {
		if !IsGeneratedOutputPath(path) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		out[path] = content
	}
	return out, nil
}

// assertLockRestored proves the one file the tree comparison exempts differs
// only by removal tombstones. A tombstone is the lock remembering that a module
// was installed, what its removal policy was, and which migration numbers it
// owns forever; strip those and the lock must be the one the derivative started
// with.
func assertLockRestored(baseline Lock, derivative string, ids []string, retained []LockedMigration) error {
	final, err := readDerivativeLock(derivative)
	if err != nil {
		return err
	}

	stripped := final
	stripped.Modules = nil
	stripped.Order = nil
	stripped.RuntimeOrders = RuntimeOrders{}
	found := make(map[string]LockedModule, len(ids))
	for _, module := range final.Modules {
		if slices.Contains(ids, module.ID) {
			found[module.ID] = module
			continue
		}
		stripped.Modules = append(stripped.Modules, module)
	}
	for _, id := range final.Order {
		if !slices.Contains(ids, id) {
			stripped.Order = append(stripped.Order, id)
		}
	}
	filterOrder := func(order []string) []string {
		out := make([]string, 0, len(order))
		for _, id := range order {
			if !slices.Contains(ids, id) {
				out = append(out, id)
			}
		}
		return out
	}
	stripped.RuntimeOrders = RuntimeOrders{
		Development: filterOrder(final.RuntimeOrders.Development),
		Test:        filterOrder(final.RuntimeOrders.Test),
		Production:  filterOrder(final.RuntimeOrders.Production),
	}
	for _, id := range ids {
		module, ok := found[id]
		if !ok {
			return fmt.Errorf("lock has no record of removed module %s; removal must leave a tombstone", id)
		}
		if module.Reason != removalTombstoneReason {
			return fmt.Errorf("lock record for removed module %s has reason %q, want %q",
				id, module.Reason, removalTombstoneReason)
		}
		if len(module.Files) != 0 || len(module.Manifest.Files) != 0 {
			return fmt.Errorf("tombstone for %s still claims %d authored file(s)", id, len(module.Files))
		}
		expected := retainedFor(retained, id, final)
		if !reflect.DeepEqual(module.Migrations, expected) {
			return fmt.Errorf("tombstone for %s carries migrations %v, want the allocated ledger %v",
				id, module.Migrations, expected)
		}
	}

	// The resolved commit is content-addressed over the tree, so a retained
	// migration necessarily moves it, and every module's source_commit moves
	// with it. That is the correct answer rather than drift: the derivative
	// genuinely is not the tree it started as. With nothing retained the commit
	// must come back exactly, and that is asserted rather than assumed.
	if final.RegistryCommit != baseline.RegistryCommit {
		if len(retained) == 0 {
			return fmt.Errorf(
				"the resolved registry commit moved to %s with no retained migration to explain it",
				final.RegistryCommit)
		}
		stripped.RegistryCommit = baseline.RegistryCommit
		for i := range stripped.Modules {
			if stripped.Modules[i].SourceCommit == final.RegistryCommit {
				stripped.Modules[i].SourceCommit = baseline.RegistryCommit
			}
			if stripped.Modules[i].SnapshotSHA256 == final.RegistryCommit {
				stripped.Modules[i].SnapshotSHA256 = baseline.RegistryCommit
			}
		}
	}
	if !reflect.DeepEqual(stripped, baseline) {
		return fmt.Errorf("the lock differs from the baseline by more than the removal tombstones")
	}
	return nil
}

// retainedFor is the migration ledger one tombstone must carry.
func retainedFor(retained []LockedMigration, id string, final Lock) []LockedMigration {
	owned := make([]LockedMigration, 0)
	for _, module := range final.Modules {
		if module.ID != id {
			continue
		}
		for _, migration := range module.Migrations {
			for _, candidate := range retained {
				if candidate.ID == migration.ID {
					owned = append(owned, candidate)
				}
			}
		}
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].ID < owned[j].ID })
	if len(owned) == 0 {
		return []LockedMigration{}
	}
	return owned
}

func readDerivativeLock(derivative string) (Lock, error) {
	data, err := os.ReadFile(filepath.Join(derivative, LockFileName))
	if err != nil {
		return Lock{}, fmt.Errorf("read derivative %s: %w", LockFileName, err)
	}
	return ParseLock(data)
}

// exampleCopyExclusions are the directory and file names a derivative must not
// carry. They are the tool-owned and machine-local paths: version control
// metadata, local secrets, build products, dependency caches, and scratch
// space. Everything else is copied, including files that are untracked, because
// a derivative must compile the tree as it is now rather than as it was last
// committed.
var exampleCopyExclusions = struct {
	relative map[string]struct{}
	names    map[string]struct{}
}{
	relative: map[string]struct{}{
		".git": {}, ".env": {}, ".superpowers": {}, "bin": {},
		"e2e/playwright-report": {}, "e2e/test-results": {},
	},
	names: map[string]struct{}{
		"tmp": {}, "node_modules": {}, ".DS_Store": {},
	},
}

func copyProjectTree(source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", destination, err)
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		slashed := filepath.ToSlash(relative)
		if _, skip := exampleCopyExclusions.relative[slashed]; skip {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if _, skip := exampleCopyExclusions.names[entry.Name()]; skip {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// A symlink in the source would either escape the copy or dangle inside
		// it; neither is a tree a derivative can build.
		if !info.Mode().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", slashed, err)
		}
		if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
			return fmt.Errorf("write %s: %w", slashed, err)
		}
		return nil
	})
}

// hashProjectTree digests every file in a derivative, keyed by slash path. The
// same exclusions apply as the copy, so a Go build cache directory or scratch
// file cannot register as drift.
func hashProjectTree(root string) (map[string]string, error) {
	out := make(map[string]string, 2048)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		slashed := filepath.ToSlash(relative)
		if _, skip := exampleCopyExclusions.relative[slashed]; skip {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if _, skip := exampleCopyExclusions.names[entry.Name()]; skip {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", slashed, err)
		}
		out[slashed] = digestBytes(content)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// runGoTool runs one go command inside the derivative. Output is captured and
// attached to the error, because a compile failure is the answer the validator
// exists to produce and a bare exit status would not name the file.
func runGoTool(ctx context.Context, derivative string, args ...string) error {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = derivative
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return nil
}
