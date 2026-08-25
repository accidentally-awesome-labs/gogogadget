package modkit_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
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

// Catalog granularity is one public renderer per installable item. A module that
// owns two renderers cannot be installed or removed independently, which is the
// whole point of a source catalog: a project that wants Badge must not be forced
// to take Banner with it.
func TestEveryUIRendererIsIndependentlyInstallable(t *testing.T) {
	lock := loadLock(t, repoRoot(t))

	fileOwner := map[string]string{}
	var uiModules int
	for _, m := range lock.Modules {
		if len(m.Manifest.Runtime.UI) == 0 {
			continue
		}
		uiModules++
		var templFiles []string
		for _, f := range m.Manifest.Files {
			if f.Class == modkit.FileClassTempl {
				templFiles = append(templFiles, f.Target)
			}
		}
		require.Len(t, templFiles, 1,
			"%s owns %d templ files; one renderer per module means one file", m.ID, len(templFiles))
		if prior, ok := fileOwner[templFiles[0]]; ok {
			t.Fatalf("%s and %s both own %s", prior, m.ID, templFiles[0])
		}
		fileOwner[templFiles[0]] = m.ID

		// The module id must name what it installs, so a reader of
		// gogogadget.json can tell which component a line selects.
		kind := "component/"
		if m.Manifest.Kind == modkit.ModuleElement {
			kind = "element/"
		}
		require.True(t, strings.HasPrefix(m.ID, kind),
			"%s declares kind %s but its id says otherwise", m.ID, m.Manifest.Kind)
	}
	require.Greater(t, uiModules, 30, "the scan found suspiciously few UI modules")
}

// Shared data contracts and helpers belong to the required core element, not to
// whichever renderer happened to be written first: a project that installs only
// Select still needs Option.
func TestSharedUIContractsBelongToCore(t *testing.T) {
	lock := loadLock(t, repoRoot(t))

	var core *modkit.LockedModule
	for i := range lock.Modules {
		if lock.Modules[i].ID == "element/ui-core" {
			core = &lock.Modules[i]
		}
	}
	require.NotNil(t, core, "element/ui-core must exist")

	owned := map[string]bool{}
	for _, f := range core.Manifest.Files {
		owned[f.Target] = true
	}
	require.True(t, owned["internal/web/templates/ui/shared.go"],
		"the shared data contracts (Option, MenuItem, Column) must be core-owned")
	require.True(t, owned["internal/web/templates/ui/enums.go"])
	require.True(t, owned["internal/web/templates/ui/attrs.go"])
	require.Empty(t, core.Manifest.Runtime.UI,
		"ui-core carries shared code, not renderers: a renderer here could not be removed")

	// Every renderer module must require core, or its file will not compile
	// after a selective install.
	for _, m := range lock.Modules {
		if len(m.Manifest.Runtime.UI) == 0 {
			continue
		}
		assert.Contains(t, m.Manifest.Requires, "element/ui-core",
			"%s uses shared types but does not require element/ui-core", m.ID)
	}
}
