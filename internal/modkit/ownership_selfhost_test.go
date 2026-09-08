// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/require"
)

// Every tracked source file must be owned by exactly one module, or removal and
// update cannot reason about it: an unowned file is never installed into a
// derivative, never verified, and never removed with the feature it belongs to.
//
// The exceptions are stated rather than pattern-matched, because each is a real
// decision: generated outputs are produced by the build (and deliberately
// excluded from the registry snapshot), migrations are pinned as migrations
// rather than files, and project scaffolding belongs to the project — a fork's
// Dockerfile is theirs to edit and the registry must never overwrite it.
func TestEveryTrackedSourceFileHasAnOwner(t *testing.T) {
	root := repoRoot(t)
	lock := loadLock(t, root)

	// Claimants, not a boolean. A `map[string]bool` answers "at least one
	// owner" while this file's own comment says exactly one: a second
	// claimant on a path sets the same key to the same value and disappears,
	// and removal then has two modules with a call on one file.
	//
	// This half states the invariant where the claim is made; it is not where
	// the invariant is won. loadLock cross-checks every lock row against its
	// manifest, so a duplicated row is refused before it reaches this map
	// ("files path ... is not owned by manifest"), and the reachable form of
	// the defect is two MANIFESTS claiming one target while never appearing
	// in the same plan. That is
	// TestThePublishedCatalogClaimsEveryTargetExactlyOnce, over the catalog
	// rather than one profile's closure.
	claimants := map[string][]string{}
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			claimants[file.Path] = append(claimants[file.Path], module.ID+" files")
		}
		for _, migration := range module.Migrations {
			claimants[migration.Path] = append(claimants[migration.Path], module.ID+" migrations")
		}
	}
	owned := make(map[string]bool, len(claimants))
	var contested []string
	for path, by := range claimants {
		owned[path] = true
		if len(by) > 1 {
			sort.Strings(by)
			contested = append(contested, path+" claimed by "+strings.Join(by, ", "))
		}
	}
	sort.Strings(contested)
	require.Empty(t, contested,
		"these paths have more than one owner, so removing either module takes a file the other still declares: %v", contested)
	require.NotEmpty(t, owned)
	catalogOwned := registryPayloadTargets(t, root)
	// Only dependency metadata and intent/lock state are project-owned. Every
	// distributable scaffold is a module payload; .ggg is ignored and therefore
	// never appears in the tracked-file inventory. `.gitignore` is NOT here:
	// ggg/system/project-base declares it, so `owned` answers for it and a
	// second entry would be a dead exemption that outlived its reason.
	projectOwned := map[string]bool{
		"go.mod": true, "go.sum": true,
		"gogogadget.json": true, "gogogadget.lock.json": true,
		".gitattributes": true,
	}

	var orphans []string
	for _, path := range trackedFiles(t, root) {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); errors.Is(err, os.ErrNotExist) {
			continue
		}
		switch {
		case owned[path], projectOwned[path], catalogOwned[path]:
			continue
		case modkit.IsGeneratedOutputPath(path):
			continue
		case path == "registry.json", path == "registry.snapshot.json", path == "registry.snapshot.sig",
			strings.HasPrefix(path, "registry/"):
			// Registry authoring metadata is the catalog plane, not installed
			// scaffold. Payload targets inside it are covered by catalogOwned.
			continue
		case strings.HasPrefix(path, ".superpowers/"):
			// Execution reports are orchestration state, not distributable
			// project source.
			continue
		}
		orphans = append(orphans, path)
	}
	require.Empty(t, orphans,
		"these source files belong to no module, so they are invisible to install, update, and removal: %v", orphans)
}
