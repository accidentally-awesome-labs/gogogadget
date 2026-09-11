package gggcli

// One regression test per CLI-side fix the task-AV dogfood round shipped
// (ledger: .superpowers/sdd/framework-followups/task-av-report.md), each born
// red by reverting its fix's hunk.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// --- P2-5: `ggg help registry init` answered "unknown command" ---

func TestHelpResolvesSubcommands(t *testing.T) {
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		out, _, err := runApp(t, t.TempDir(), nil, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return out
	}

	t.Run("help COMMAND SUB prints the subcommand's usage", func(t *testing.T) {
		out := run(t, "help", "registry", "init")
		for _, want := range []string{"registry init", "ggg registry init --namespace", "Usage"} {
			if !strings.Contains(out, want) {
				t.Fatalf("help registry init missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "unknown command") {
			t.Fatalf("the documented help form is still refused:\n%s", out)
		}
	})

	t.Run("help COMMAND lists every subcommand", func(t *testing.T) {
		out := run(t, "help", "registry")
		if !strings.Contains(out, "Subcommands:") {
			t.Fatalf("registry help hides its subcommands:\n%s", out)
		}
		for _, sub := range []string{"build", "validate", "init", "keygen", "sign", "verify", "rotate", "add", "remove", "update"} {
			if !strings.Contains(out, "ggg registry "+sub) {
				t.Fatalf("registry help missing the %s form:\n%s", sub, out)
			}
		}
	})

	t.Run("an unknown subcommand is refused as one", func(t *testing.T) {
		out := run(t, "help", "registry", "bogus")
		if !strings.Contains(out, "unknown registry subcommand") {
			t.Fatalf("unknown subcommand is not named as one:\n%s", out)
		}
	})

	t.Run("a command without subcommands keeps its flag list", func(t *testing.T) {
		out := run(t, "help", "sync")
		if !strings.Contains(out, "--check") || strings.Contains(out, "Subcommands:") {
			t.Fatalf("plain command help changed shape:\n%s", out)
		}
	})
}

// --- P2-4: `registry build` split "my manifest is invalid" across exit 3 and exit 1 ---

func TestRegistryBuildAuthoringRefusalsShareExitCode3(t *testing.T) {
	t.Run("a manifest that cannot decode", func(t *testing.T) {
		root := selfHostTree(t)
		writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":`))
		_, _, err := runApp(t, root, nil, "registry", "build")
		if err == nil {
			t.Fatal("build accepted an undecodable manifest")
		}
		if got := exitOf(t, err); got != 3 {
			t.Fatalf("decode refusal exit = %d, want 3 (one authoring class): %v", got, err)
		}
	})

	t.Run("a manifest invariant the decode cannot see", func(t *testing.T) {
		root := selfHostTree(t)
		widget := `{"schema":2,"module":{
			"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
			"title":"Widget","description":"A widget system.","requires":[],
			"files":[
				{"source":"internal/widget/zed.go","target":"internal/widget/zed.go","class":"go","contract":false,
				 "sha256":"` + sha256Hex([]byte("package widget\n")) + `","rewrite_module":true},
				{"source":"internal/widget/widget.go","target":"internal/widget/widget.go","class":"go","contract":true,
				 "sha256":"` + sha256Hex([]byte("package widget\n\nconst Version = 1\n")) + `","rewrite_module":true}],
			"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
			"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`
		writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(widget))
		writeTestFile(t, root, "internal/widget/zed.go", []byte("package widget\n"))
		_, _, err := runApp(t, root, nil, "registry", "build")
		if err == nil {
			t.Fatal("build accepted an unsorted files array")
		}
		if got := exitOf(t, err); got != 3 {
			t.Fatalf("invariant refusal exit = %d, want 3 (one authoring class): %v", got, err)
		}
		if !strings.Contains(err.Error(), "sorted by target") {
			t.Fatalf("refusal does not name the rule: %v", err)
		}
	})

	t.Run("a clean tree still builds", func(t *testing.T) {
		root := selfHostTree(t)
		if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
			t.Fatalf("clean build: %v", err)
		}
	})
}

// --- P2-1, CLI half: the publisher tree's own gate ---

// The dogfood's pre-publish shape: a standalone repository with a signed
// snapshot and no lock, a payload edited without a revision bump, and a
// `registry build` that used to absorb the edit silently.
func TestRegistryBuildRefusesAMovedPayloadAgainstTheSignedSnapshot(t *testing.T) {
	root := selfHostTree(t)
	if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
		t.Fatalf("first build: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "registry.snapshot.json")); err != nil {
		t.Fatalf("the first build wrote no snapshot: %v", err)
	}
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Version = 2 // edited, revision forgotten\n"))
	_, _, err := runApp(t, root, nil, "registry", "build")
	if err == nil {
		t.Fatal("build absorbed a payload change at an unchanged revision")
	}
	if got := exitOf(t, err); got != 3 {
		t.Fatalf("refusal exit = %d, want 3: %v", got, err)
	}
	for _, want := range []string{"revision", "ggg/system/widget"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
}

// indexedRegistryTree is a directory registry whose indexes already name
// their module, so a catalog load — not a build — resolves it.
func indexedRegistryTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "profiles"} {
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":2,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/systems.json",
		[]byte(`{"schema":2,"kind":"system","items":["registry/modules/system/widget/module.json"]}`))
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],
		"files":[{"source":"internal/widget/widget.go","target":"internal/widget/widget.go","class":"go",
		          "sha256":"`+sha256Hex([]byte("package widget\n\nconst Version = 1\n"))+`","rewrite_module":true,"contract":true}],
		"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Version = 1\n"))
	return root
}

func TestNewHonorsAnAbsoluteDirectoryRegistry(t *testing.T) {
	registry := indexedRegistryTree(t)
	// The controller's root is a DIFFERENT tree with no registry of its own:
	// pre-fix, the absolute path was joined under this root and the resolve
	// answered with whatever the project directory contained.
	project := t.TempDir()
	writeTestFile(t, project, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	controller := NewController(ControllerOptions{Root: project})

	t.Run("an absolute path resolves that tree", func(t *testing.T) {
		_, snapshot, catalog, err := controller.resolveNewCatalog(context.Background(), modkit.ProjectRegistry{
			Namespace: "ggg", Source: "directory", Path: filepath.ToSlash(registry),
		})
		if err != nil {
			t.Fatalf("resolveNewCatalog(absolute): %v", err)
		}
		if len(catalog.Modules) != 1 || catalog.Modules[0].ID != "ggg/system/widget" {
			t.Fatalf("absolute catalog = %d modules, want the widget registry's one", len(catalog.Modules))
		}
		if snapshot.Commit == "" {
			t.Fatal("absolute snapshot carries no commit")
		}
	})

	t.Run("a relative path still resolves against the project root", func(t *testing.T) {
		writeTestFile(t, project, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"example.com/acme/app","includes":[`+
			`"registry/elements.json","registry/components.json","registry/pages.json",`+
			`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
		for _, kind := range []string{"elements", "components", "pages", "workflows", "systems", "profiles"} {
			writeTestFile(t, project, "registry/"+kind+".json",
				[]byte(`{"schema":2,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
		}
		_, _, catalog, err := controller.resolveNewCatalog(context.Background(), modkit.ProjectRegistry{
			Namespace: "ggg", Source: "directory", Path: ".",
		})
		if err != nil {
			t.Fatalf("resolveNewCatalog(relative): %v", err)
		}
		if len(catalog.Modules) != 0 {
			t.Fatalf("relative catalog = %d modules, want the empty registry it names", len(catalog.Modules))
		}
	})
}

// --- P2-9: `registry remove` deadlocked on its own removal tombstones ---

func TestRegistryRemoveSequencesItsOwnTombstones(t *testing.T) {
	extFixture := func(t *testing.T) fstest.MapFS {
		t.Helper()
		files := fstest.MapFS{}
		putJSON(t, files, "registry.json", map[string]any{
			"schema": 2, "namespace": "ext", "canonical_module": "example.com/ext/registry",
			"includes": []string{
				"registry/elements.json", "registry/components.json", "registry/pages.json",
				"registry/workflows.json", "registry/systems.json", "registry/profiles.json",
			},
		})
		for _, index := range []modkit.CatalogIndex{
			{Schema: 2, Kind: modkit.CatalogElement, Items: []string{"registry/modules/element/gone/module.json"}},
			{Schema: 2, Kind: modkit.CatalogComponent, Items: []string{}},
			{Schema: 2, Kind: modkit.CatalogPage, Items: []string{}},
			{Schema: 2, Kind: modkit.CatalogWorkflow, Items: []string{}},
			{Schema: 2, Kind: modkit.CatalogSystem, Items: []string{}},
			{Schema: 2, Kind: modkit.CatalogProfile, Items: []string{}},
		} {
			putJSON(t, files, "registry/"+string(index.Kind)+"s.json", index)
		}
		goneContent := []byte("package gone\n\nconst Version = 1\n")
		gone := baseModule("ext/element/gone", "element", "gone")
		gone.Files = []modkit.ManifestFile{{
			Source: "registry/modules/element/gone/gone.go", Target: "internal/modules/gone.go",
			Class: modkit.FileClassGo, SHA256: sha256Hex(goneContent),
		}}
		putJSON(t, files, "registry/modules/element/gone/module.json", modkit.ModuleDocument{Schema: 2, Module: gone})
		files[gone.Files[0].Source] = &fstest.MapFile{Data: goneContent}
		return files
	}
	t.Helper()

	core := fixtureRegistry(t)
	ext := extFixture(t)
	source := refSource{snapshots: map[string]modkit.Snapshot{
		"main": {Commit: testCommitA, FS: core},
		"ext":  {Commit: testCommitB, FS: ext},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema: 2,
		Registries: []modkit.ProjectRegistry{
			{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: testKeyA},
			{Namespace: "ext", Source: "github", Repository: "ext/registry", Ref: "ext", PublicKey: testKeyA},
		},
		Providers: map[string]modkit.ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/profile/full", "ext/element/gone"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	engine := modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})

	if _, _, err := runApp(t, root, engine, "sync"); err != nil {
		t.Fatalf("sync(initial): %v", err)
	}
	// Deselection IS the removal: the tombstone lands in exclude.
	if _, _, err := runApp(t, root, engine, "remove", "ext/element/gone"); err != nil {
		t.Fatalf("remove ext module: %v", err)
	}
	intentAfterRemove, err := os.ReadFile(filepath.Join(root, modkit.ProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	var removed modkit.Project
	if err := json.Unmarshal(intentAfterRemove, &removed); err != nil {
		t.Fatal(err)
	}
	if len(removed.Exclude) != 1 || removed.Exclude[0] != "ext/element/gone" {
		t.Fatalf("remove did not write the tombstone: %#v", removed.Exclude)
	}

	// The deadlock itself: removing the registry must take its tombstones.
	if _, _, err := runApp(t, root, engine, "registry", "remove", "ext"); err != nil {
		t.Fatalf("registry remove deadlocked on its own tombstone: %v", err)
	}
	final, err := os.ReadFile(filepath.Join(root, modkit.ProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	var project modkit.Project
	if err := json.Unmarshal(final, &project); err != nil {
		t.Fatal(err)
	}
	if len(project.Registries) != 1 || project.Registries[0].Namespace != "ggg" {
		t.Fatalf("registries after remove = %#v", project.Registries)
	}
	if len(project.Exclude) != 0 {
		t.Fatalf("exclude after registry remove = %#v, want the ext tombstone gone", project.Exclude)
	}
	// The follow-up plan is the real claim: no hand edit, no deadlock.
	if _, _, err := runApp(t, root, engine, "sync"); err != nil {
		t.Fatalf("sync after registry remove: %v", err)
	}
}
