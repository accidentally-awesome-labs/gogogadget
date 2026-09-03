package modkit_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func registryPayloadTargets(t *testing.T, root string) map[string]bool {
	t.Helper()
	owned := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "registry", "modules"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "module.json" {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc struct {
			Module struct {
				Files []struct {
					Target string `json:"target"`
				} `json:"files"`
			} `json:"module"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		for _, file := range doc.Module.Files {
			owned[file.Target] = true
		}
		return nil
	})
	require.NoError(t, err)
	return owned
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}

// trackedFiles lists every file git would carry, INCLUDING files that are new
// and not yet added. `git ls-files` alone sees only what is already tracked, so a
// freshly written source file is invisible to the ownership check until someone
// commits it - which moves the failure from the moment the file was created to
// whenever the next commit happens, usually in someone else's run. Four unowned
// test files hid behind exactly that gap today.
//
// --others adds untracked paths and --exclude-standard applies .gitignore, so
// build output and local scratch stay out.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	out, err := cmd.Output()
	require.NoError(t, err)

	// The two lists overlap for nothing today, but --cached and --others are
	// separate enumerations and a rename in flight can appear in both.
	seen := map[string]bool{}
	var paths []string
	for _, path := range strings.Fields(string(out)) {
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
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
		kind := "ggg/component/"
		if m.Manifest.Kind == modkit.ModuleElement {
			kind = "ggg/element/"
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
		if lock.Modules[i].ID == "ggg/element/ui-core" {
			core = &lock.Modules[i]
		}
	}
	require.NotNil(t, core, "ggg/element/ui-core must exist")

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
		found := false
		for _, requirement := range m.Manifest.Requires {
			if requirement.ID == "ggg/element/ui-core" {
				found = true
				break
			}
		}
		assert.Truef(t, found, "%s uses shared types but does not require element/ui-core", m.ID)
	}
}

// An applied migration is the one artifact in this system that can never be
// corrected. The database already ran it, so editing the file in place makes the
// recorded schema history disagree with every deployment that applied the old
// text - and nothing in the tree reports the divergence. Later migrations are
// written against a schema that no longer matches what the file claims to build.
//
// The engine's immutability rules are covered against synthetic fixtures
// elsewhere. This asserts the property that actually protects users: the real
// ledger in this repository. Every migration on disk is claimed by exactly one
// module, declared immutable, and byte-identical to the digest recorded when it
// was allocated. A forward-only fix is a new migration, never an edit.
func TestRepositoryMigrationLedgerIsImmutableAndOwned(t *testing.T) {
	root := repoRoot(t)
	lock := loadLock(t, root)

	type claim struct {
		module string
		kind   string
		digest string
	}
	claims := map[string]claim{}
	for _, module := range lock.Modules {
		// The lock records the immutable allocation - id, global number, path
		// and digest. The forward-only kind is the module's own declaration, so
		// it comes from the manifest the lock carries alongside it.
		kinds := map[string]string{}
		for _, declared := range module.Manifest.Migrations {
			kinds[declared.ID] = string(declared.Kind)
		}
		for _, migration := range module.Migrations {
			id := strings.TrimSuffix(filepath.Base(migration.Path), ".sql")
			if prior, ok := claims[id]; ok {
				t.Fatalf("migration %q is claimed by both %s and %s: two owners means neither can remove it safely",
					id, prior.module, module.ID)
			}
			claims[id] = claim{module: module.ID, kind: kinds[migration.ID], digest: migration.SHA256}
		}
	}
	require.NotEmpty(t, claims, "the lock records no migrations at all, so this test proves nothing")

	onDisk, err := filepath.Glob(filepath.Join(root, "internal", "db", "migrations", "*.sql"))
	require.NoError(t, err)
	require.NotEmpty(t, onDisk)

	seen := map[string]bool{}
	for _, path := range onDisk {
		id := strings.TrimSuffix(filepath.Base(path), ".sql")
		seen[id] = true

		owner, ok := claims[id]
		require.Truef(t, ok,
			"migration %q exists on disk but no module claims it, so installing that module into a derivative would silently omit a schema change", id)

		assert.Equalf(t, "immutable", owner.kind,
			"migration %q is claimed by %s as %q; an applied migration is immutable by definition", id, owner.module, owner.kind)

		body, err := os.ReadFile(path)
		require.NoError(t, err)
		sum := sha256.Sum256(body)
		assert.Equalf(t, owner.digest, hex.EncodeToString(sum[:]),
			"migration %q was edited after it was allocated by %s. The database already ran the previous text, so this file no longer describes the schema any deployment has. Revert it and add a new forward migration instead.",
			id, owner.module)
	}

	for id, owner := range claims {
		assert.Truef(t, seen[id],
			"%s claims migration %q but no such file exists, so a derivative installing it would fail to migrate", owner.module, id)
	}
}
