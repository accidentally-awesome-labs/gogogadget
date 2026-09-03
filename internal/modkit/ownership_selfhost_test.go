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

	owned := map[string]bool{}
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			owned[file.Path] = true
		}
		for _, migration := range module.Migrations {
			owned[migration.Path] = true
		}
	}
	require.NotEmpty(t, owned)
	catalogOwned := registryPayloadTargets(t, root)
	// Only dependency metadata and intent/lock state are project-owned. Every
	// distributable scaffold is a module payload; .ggg is ignored and therefore
	// never appears in the tracked-file inventory.
	projectOwned := map[string]bool{
		"go.mod": true, "go.sum": true,
		"gogogadget.json": true, "gogogadget.lock.json": true,
		".gitattributes": true, ".gitignore": true,
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
