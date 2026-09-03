package gggcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The default --dir must resolve to the directory that actually holds
// registry.json. This repository declares a directory registry at path
// "registry" while its registry.json and registry.snapshot.json live at the
// root, and returning the declared path made `ggg registry sign` refuse with
// "read registry.json: open registry.json: no such file or directory" on the
// tree that publishes the core catalog — the one command the release order in
// content/docs/extending.md depends on. Only `--dir .` worked, which is a
// default that is wrong exactly where it matters.
func TestRegistrySigningDefaultsToTheDirectoryHoldingRegistryJSON(t *testing.T) {
	root := selfHostTree(t)
	project, err := modkit.MarshalProject(modkit.Project{
		Schema: 2, Modules: []string{}, Exclude: []string{},
		Providers:  map[string]modkit.ProviderSelections{},
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "directory", Path: "registry"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, project)
	if _, err := os.Stat(filepath.Join(root, "registry", "registry.json")); !os.IsNotExist(err) {
		t.Fatalf("fixture is not the root-level layout under test: %v", err)
	}

	private := filepath.Join(t.TempDir(), "registry.key")
	public := filepath.Join(t.TempDir(), "registry.pub")
	if _, _, err := runApp(t, root, nil, "registry", "keygen", "--private", private, "--public", public); err != nil {
		t.Fatalf("registry keygen: %v", err)
	}
	if _, _, err := runApp(t, root, nil, "registry", "sign", "--key-file", private); err != nil {
		t.Fatalf("registry sign without --dir: %v", err)
	}
	for _, name := range []string{modkit.RegistrySnapshotPath, modkit.RegistrySignaturePath} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("sign did not write %s beside registry.json: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(root, "registry", name)); !os.IsNotExist(err) {
			t.Fatalf("sign wrote %s into the index directory instead of the registry root", name)
		}
	}
	key, err := os.ReadFile(public)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runApp(t, root, nil, "registry", "verify", "--public-key", string(key)); err != nil {
		t.Fatalf("registry verify without --dir: %v", err)
	}
}

// repoRootFromTest walks up to the project root. The test asserts on committed
// artifacts, so it needs the real tree rather than a fixture.
func repoRootFromTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "gogogadget.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no gogogadget.json above the test working directory")
		}
		dir = parent
	}
}
