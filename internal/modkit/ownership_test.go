package modkit_test

import (
	"os"
	"os/exec"
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

	// Project-owned scaffolding: build, deploy, and tooling files a derivative
	// edits freely, plus the project's own registry state.
	projectOwned := map[string]bool{
		".air.toml": true, ".gitignore": true, ".vscode/extensions.json": true,
		"Dockerfile": true, "LICENSE": true, "Makefile": true, "compose.yaml": true,
		"fly.toml": true, "go.mod": true, "go.sum": true,
		"gogogadget.json": true, "gogogadget.lock.json": true,
		".env.example": true, "registry.json": true,
	}

	var orphans []string
	for _, path := range trackedFiles(t, root) {
		switch {
		case owned[path], projectOwned[path]:
			continue
		case modkit.IsGeneratedOutputPath(path):
			continue
		case strings.HasPrefix(path, "registry/"), strings.HasPrefix(path, "content/"),
			strings.HasPrefix(path, "e2e/"), strings.HasPrefix(path, "docs/"),
			strings.HasPrefix(path, ".github/"), strings.HasPrefix(path, "scripts/"),
			strings.HasSuffix(path, ".md"):
			continue
		}
		orphans = append(orphans, path)
	}
	require.Empty(t, orphans,
		"these tracked source files belong to no module, so they are invisible to install, update, and removal: %v", orphans)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}

func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err)
	return strings.Fields(string(out))
}

func loadLock(t *testing.T, root string) modkit.Lock {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "gogogadget.lock.json"))
	require.NoError(t, err)
	lock, err := modkit.ParseLock(raw)
	require.NoError(t, err)
	return lock
}
