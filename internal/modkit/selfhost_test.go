// Self-host assertions. This file is declared self_host by ggg/system/modkit:
// the repository that publishes the registry installs and runs it, and no
// derivative ever receives it. Everything here asserts about THIS repository —
// its committed snapshot signature, its example and external fixtures, its CI
// workflows, its vendored bytes, its ownership sweep — never about the source
// the registry distributes.

package modkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selfHostDeclarations returns every self_host payload the published catalog
// declares, keyed by owning module.
func selfHostDeclarations(t *testing.T) (Catalog, map[string][]ManifestFile) {
	t.Helper()
	catalog, err := LoadCatalog(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("load published catalog: %v", err)
	}
	declared := map[string][]ManifestFile{}
	for _, module := range catalog.Modules {
		for _, file := range module.Files {
			if file.SelfHost {
				declared[module.ID] = append(declared[module.ID], file)
			}
		}
	}
	return catalog, declared
}

// Guard 3. A self_host payload that names a file nobody has is a test that runs
// nowhere: skipped in every derivative by declaration and absent from the
// publishing repository by accident. Nothing else would notice — the payload
// digest is only read when the file is installed — so the set is asserted
// non-empty and every member is required to exist on both sides of the
// declaration.
func TestSelfHostPayloadsAreDeclaredAndPresent(t *testing.T) {
	catalog, declared := selfHostDeclarations(t)
	if len(declared) == 0 {
		t.Fatal("no module declares a self_host payload; the core repository's own assertions would then ship into every derivative")
	}
	total := 0
	for id, files := range declared {
		for _, file := range files {
			total++
			if file.Class != FileClassTest {
				t.Errorf("%s self_host payload %s class = %q, want %q", id, file.Target, file.Class, FileClassTest)
			}
			for _, path := range []string{file.Source, file.Target} {
				if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(path))); err != nil {
					t.Errorf("%s declares self_host payload %s which is not in the tree: %v", id, path, err)
				}
			}
			if !strings.HasSuffix(file.Target, "_test.go") && !strings.Contains(file.Target, "/testdata/") {
				t.Errorf("%s self_host payload %s is neither a test file nor testdata", id, file.Target)
			}
		}
	}
	if total == 0 {
		t.Fatal("self_host declarations resolved to no files")
	}

	// The mechanism only installs these here because this repository IS the
	// registry's canonical module. If that ever stops being true, the core gate
	// silently loses every assertion below.
	goMod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	modulePath := ""
	for _, line := range strings.Split(string(goMod), "\n") {
		if rest, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			modulePath = strings.TrimSpace(rest)
			break
		}
	}
	if !InstallsSelfHostPayloads(modulePath, catalog.CanonicalModule) {
		t.Fatalf("module path %q is not the published canonical module %q, so this repository would install none of its own self_host payloads",
			modulePath, catalog.CanonicalModule)
	}
}

// Guard 1. The point of the field is that a derivative stops receiving these,
// not that they stop running: the publishing repository must still install and
// run every one. The committed lock is what "installed here" means, so every
// declared self_host payload has to be a recorded, digest-pinned install in it.
func TestCoreRepositoryInstallsEverySelfHostPayload(t *testing.T) {
	_, declared := selfHostDeclarations(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "gogogadget.lock.json"))
	if err != nil {
		t.Fatalf("read committed lock: %v", err)
	}
	lock, err := ParseLock(raw)
	if err != nil {
		t.Fatalf("parse committed lock: %v", err)
	}
	locked := map[string]map[string]LockedFile{}
	for _, module := range lock.Modules {
		byPath := map[string]LockedFile{}
		for _, file := range module.Files {
			byPath[file.Path] = file
		}
		locked[module.ID] = byPath
	}
	for id, files := range declared {
		installed, ok := locked[id]
		if !ok {
			t.Errorf("module %s declares self_host payloads but is not installed in the committed lock", id)
			continue
		}
		for _, file := range files {
			row, ok := installed[file.Target]
			if !ok {
				t.Errorf("self_host payload %s of %s is declared but not installed here; the core gate no longer runs it", file.Target, id)
				continue
			}
			if row.State == FileGenerated || row.BaseSHA256 == "" {
				t.Errorf("self_host payload %s of %s is recorded as %q with base %q, want a pinned install", file.Target, id, row.State, row.BaseSHA256)
			}
			if _, err := os.Stat(filepath.Join("..", "..", filepath.FromSlash(file.Target))); err != nil {
				t.Errorf("self_host payload %s of %s is locked as installed but not on disk: %v", file.Target, id, err)
			}
		}
	}
}
