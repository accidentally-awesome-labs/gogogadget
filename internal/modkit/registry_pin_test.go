package modkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gogogadget.json holds the ref a human maintains, and the plan sanctions a
// release tag ("core GitHub defaults release tag, never main"). A tag is not a
// commit, and offline GitHub resolution cannot make one, so before this every
// project created against a release tag failed its own first documented
// command: `ggg setup` runs `sync --offline` and refused with "ref must be a
// full 40-character lowercase commit". The lock already recorded the commit
// that tag resolved to; resolution simply never consulted it.

// tagSource mimics GitHubSource's two arms: it maps refs to commits when it can
// reach the network and refuses anything that is not a commit when it cannot.
// It records every ref it was asked for, so a test can prove which one the
// engine actually requested.
type tagSource struct {
	commits  map[string]string // ref (tag or commit) -> commit
	trees    map[string]Snapshot
	offline  bool
	askedFor []string
}

func (s *tagSource) Resolve(_ context.Context, registry ProjectRegistry) (Snapshot, error) {
	s.askedFor = append(s.askedFor, registry.Ref)
	if s.offline && !validGitHubCommit(registry.Ref) {
		return Snapshot{}, fmt.Errorf(
			"resolve GitHub source %q offline: ref %q is not a full 40-character lowercase commit",
			registry.Repository, registry.Ref)
	}
	commit, ok := s.commits[registry.Ref]
	if !ok {
		return Snapshot{}, fmt.Errorf("unknown ref %q", registry.Ref)
	}
	snapshot, ok := s.trees[commit]
	if !ok {
		return Snapshot{}, fmt.Errorf("no tree for commit %q", commit)
	}
	snapshot.Commit = commit
	return snapshot, nil
}

// tagPinnedProject installs a registry pinned to a release tag and returns the
// project root, the source, and the commit the lock recorded.
func tagPinnedProject(t *testing.T) (string, *tagSource, string) {
	t.Helper()
	const (
		tag       = "v0.1.0"
		published = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	files, _ := removalRegistries(t)
	source := &tagSource{
		commits: map[string]string{tag: published, published: published},
		trees:   map[string]Snapshot{published: {Commit: published, FS: files}},
	}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: tag,
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}},
		Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/optional"}, Exclude: []string{},
	})
	// The first sync is the one that has network access, exactly as a genesis
	// does. It is what records the commit the tag resolved to.
	engine := New(Options{Source: source, Generator: &scriptedGenerator{}})
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err != nil {
		t.Fatalf("Plan(online install): %v", err)
	}
	if _, err := engine.Apply(context.Background(), plan); err != nil {
		t.Fatalf("Apply(online install): %v", err)
	}
	record, ok := lockedSnapshotFor(plan.Lock, "ggg")
	if !ok || record.Commit != published {
		t.Fatalf("lock recorded %#v, want commit %s", record, published)
	}
	// The operator's tag stays in the intent file: it is what a human
	// maintains and what `ggg update --ref` compares against.
	if plan.Project.Registries[0].Ref != tag {
		t.Fatalf("project ref = %q, want the operator's tag %q", plan.Project.Registries[0].Ref, tag)
	}
	return root, source, published
}

// The gate: a tag-pinned project must be able to run its own `make setup`,
// which is `sync --offline`.
func TestOfflineSyncResolvesATagPinnedRegistryFromTheLock(t *testing.T) {
	root, source, published := tagPinnedProject(t)
	source.offline = true
	source.askedFor = nil

	engine := New(Options{Source: source, Generator: &scriptedGenerator{}})
	plan, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync, Offline: true})
	if err != nil {
		t.Fatalf("Plan(offline sync) on a tag-pinned project: %v", err)
	}
	if len(source.askedFor) != 1 || source.askedFor[0] != published {
		t.Fatalf("offline resolution asked for %v, want the lock's recorded commit %s", source.askedFor, published)
	}
	if plan.Lock.Registries[0].RequestedRef != "v0.1.0" {
		t.Fatalf("lock requested_ref = %q, want the operator's tag", plan.Lock.Registries[0].RequestedRef)
	}
}

// Without a record there is nothing to resolve a tag through, so the refusal
// has to name the two commands that can produce one.
func TestOfflineSyncWithoutARecordRefusesAndNamesTheRemedy(t *testing.T) {
	files, _ := removalRegistries(t)
	source := &tagSource{
		commits: map[string]string{"v0.1.0": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		trees:   map[string]Snapshot{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": {FS: files}},
		offline: true,
	}
	root := writeTargetProject(t, "example.com/acme/app", Project{
		Schema: 2,
		Registries: []ProjectRegistry{{
			Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "v0.1.0",
			PublicKey: "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg=",
		}},
		Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/optional"}, Exclude: []string{},
	})
	_, err := New(Options{Source: source, Generator: &scriptedGenerator{}}).
		Plan(context.Background(), root, Operation{Kind: OpSync, Offline: true})
	if err == nil {
		t.Fatal("offline sync succeeded with no recorded snapshot and a tag ref")
	}
	if !strings.Contains(err.Error(), "v0.1.0") {
		t.Fatalf("refusal does not name the ref: %v", err)
	}
}

// A commit-pinned registry that resolves to something else is a pin the lock
// and the intent disagree about. Sync must refuse rather than move it; that is
// what `ggg update` is for.
func TestSyncRefusesACommitPinThatDisagreesWithTheLock(t *testing.T) {
	root, source, published := tagPinnedProject(t)
	// The operator hand-edits the ref to a different commit.
	const other = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	files, _ := removalRegistries(t)
	source.commits[other] = other
	source.trees[other] = Snapshot{FS: files}
	raw, err := os.ReadFile(filepath.Join(root, ProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	project, err := ParseProject(raw)
	if err != nil {
		t.Fatal(err)
	}
	project.Registries[0].Ref = other
	edited, err := MarshalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, ProjectFileName, edited)

	engine := New(Options{Source: source, Generator: &scriptedGenerator{}})
	_, err = engine.Plan(context.Background(), root, Operation{Kind: OpSync})
	if err == nil {
		t.Fatal("sync moved a pinned project silently")
	}
	if !strings.Contains(err.Error(), published) || !strings.Contains(err.Error(), "ggg update") {
		t.Fatalf("refusal must name the recorded commit and the remedy: %v", err)
	}

	// Update is the sanctioned mover, and after it the lock records the new
	// commit — so the next offline sync follows the new pin, not the old one.
	moved, err := engine.Plan(context.Background(), root,
		Operation{Kind: OpUpdate, TargetedRegistry: "ggg", RegistryRef: other})
	if err != nil {
		t.Fatalf("Plan(update --registry ggg --ref %s): %v", other, err)
	}
	if _, err := engine.Apply(context.Background(), moved); err != nil {
		t.Fatalf("Apply(update): %v", err)
	}
	if record, ok := lockedSnapshotFor(moved.Lock, "ggg"); !ok || record.Commit != other {
		t.Fatalf("lock after update recorded %#v, want commit %s", record, other)
	}
	source.offline = true
	source.askedFor = nil
	if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync, Offline: true}); err != nil {
		t.Fatalf("offline sync after update: %v", err)
	}
	if len(source.askedFor) != 1 || source.askedFor[0] != other {
		t.Fatalf("offline resolution asked for %v, want the re-pinned commit %s", source.askedFor, other)
	}
}

// `ggg update --registry NS --ref REF` moves a tag pin, and the lock must
// record what the new tag resolved to so the next offline sync follows it.
func TestUpdateRefRePinsSoTheNextOfflineSyncFollows(t *testing.T) {
	root, source, published := tagPinnedProject(t)
	const (
		nextTag    = "v0.2.0"
		nextCommit = "cccccccccccccccccccccccccccccccccccccccc"
	)
	files, _ := removalRegistries(t)
	source.commits[nextTag] = nextCommit
	source.commits[nextCommit] = nextCommit
	source.trees[nextCommit] = Snapshot{FS: files}

	engine := New(Options{Source: source, Generator: &scriptedGenerator{}})
	moved, err := engine.Plan(context.Background(), root,
		Operation{Kind: OpUpdate, TargetedRegistry: "ggg", RegistryRef: nextTag})
	if err != nil {
		t.Fatalf("Plan(update --registry ggg --ref %s): %v", nextTag, err)
	}
	if _, err := engine.Apply(context.Background(), moved); err != nil {
		t.Fatalf("Apply(update): %v", err)
	}
	if record, ok := lockedSnapshotFor(moved.Lock, "ggg"); !ok || record.Commit != nextCommit {
		t.Fatalf("lock after update recorded %#v, want commit %s", record, nextCommit)
	}
	if moved.Project.Registries[0].Ref != nextTag {
		t.Fatalf("project ref = %q, want the new tag %q", moved.Project.Registries[0].Ref, nextTag)
	}

	source.offline = true
	source.askedFor = nil
	if _, err := engine.Plan(context.Background(), root, Operation{Kind: OpSync, Offline: true}); err != nil {
		t.Fatalf("offline sync after update --ref: %v", err)
	}
	if len(source.askedFor) != 1 || source.askedFor[0] != nextCommit {
		t.Fatalf("offline resolution asked for %v, want the new commit %s (old was %s)",
			source.askedFor, nextCommit, published)
	}
}
