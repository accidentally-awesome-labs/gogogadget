package modkit

import (
	"context"
	"fmt"
)

// ProjectSource resolves every registry kind a project may declare. Directory
// registries are always rooted at the project and therefore cannot escape it;
// GitHub registries retain the signed archive/cache behavior of GitHubSource.
type ProjectSource struct {
	Root   string
	GitHub GitHubSource
}

func (s ProjectSource) Resolve(ctx context.Context, registry ProjectRegistry) (Snapshot, error) {
	switch registry.Source {
	case "directory":
		return (DirectorySource{Root: s.Root}).Resolve(ctx, registry)
	case "github":
		return s.GitHub.Resolve(ctx, registry)
	default:
		return Snapshot{}, fmt.Errorf("unsupported registry source %q", registry.Source)
	}
}

// lockedSnapshotFor returns the snapshot record a lock holds for one registry
// namespace. Targeted updates may leave modules on older snapshots, but a
// namespace has exactly one current record.
func lockedSnapshotFor(lock Lock, namespace string) (LockedSnapshot, bool) {
	for _, record := range lock.Snapshots {
		if record.Namespace == namespace {
			return record, record.Commit != ""
		}
	}
	return LockedSnapshot{}, false
}

// resolveConfiguredRegistry resolves one configured registry against the
// commit the lock already recorded for it.
//
// gogogadget.json holds what a human maintains — `"ref": "v0.1.0"` — and the
// plan sanctions exactly that ("core GitHub defaults release tag, never
// main"). A release tag is not a commit, and offline GitHub resolution cannot
// turn one into a commit, so before this a tag-pinned project failed its own
// first documented command: `ggg setup` runs `sync --offline` and refused with
// "ref must be a full 40-character lowercase commit".
//
// Nothing was missing, only unconsulted. The lock records the commit the tag
// resolved to, and the cache entry is content-addressed by the snapshot digest
// that was signature-verified when it was fetched. So the pin is the lock, and
// the ref stays the operator's tag — rewriting it to a commit in
// gogogadget.json would make "which release am I on?" unanswerable and leave
// `ggg update --registry NS --ref REF` nothing to compare against.
//
// preferRecorded asks for the recorded commit instead of the ref. Offline
// resolution must set it, because GitHub cannot turn a ref into a commit
// without the network; conflict replay sets it too, since replaying a pending
// candidate must not pull a newly moved upstream into the other registries.
// Online, an unset preferRecorded still resolves the ref, because that is the
// only way a moved tag is noticed at all.
//
// When verify is set, a registry whose configured ref is a full commit must
// resolve to the commit the lock records; a disagreement is a refusal naming
// `ggg update`. It is scoped to commit refs on purpose. A symbolic ref is a
// declaration that the project follows something mutable — `main` is the
// clear case, and the engine's own tests document that sync advances with it —
// so refusing a moved symbolic ref would break ref-tracking projects and
// `ggg registry add`. A commit ref that disagrees with the lock is different:
// nothing resolved it, someone edited it, and moving a pin is what `ggg
// update` is for.
func (e *Engine) resolveConfiguredRegistry(
	ctx context.Context, configured ProjectRegistry, lock Lock, preferRecorded, verify bool,
) (Snapshot, error) {
	record, pinned := lockedSnapshotFor(lock, configured.Namespace)
	commitRef := validGitHubCommit(configured.Ref)
	if configured.Source == "github" && pinned && preferRecorded && !commitRef {
		configured.Ref, commitRef = record.Commit, true
	}
	snapshot, err := e.source.Resolve(ctx, configured)
	if err != nil {
		return Snapshot{}, err
	}
	if verify && pinned && commitRef && snapshot.Commit != record.Commit {
		return Snapshot{}, fmt.Errorf(
			"registry %q is pinned to commit %s but the lock records %s; "+
				"run `ggg update --registry %s --ref %s` to move the pin deliberately",
			configured.Namespace, snapshot.Commit, record.Commit,
			configured.Namespace, configured.Ref)
	}
	return snapshot, nil
}
