package gggcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// The CLI tests run against a small offline fixture registry resolved from a
// stub source, so no test touches the network or the host registry cache.

const (
	testCommitA = "0123456789abcdef0123456789abcdef01234567"
	testCommitB = "89abcdef0123456789abcdef0123456789abcdef"
	testDigestA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testKeyA    = "A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="
)

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func writeTestFile(t *testing.T, root, name string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func putJSON(t *testing.T, files fstest.MapFS, name string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	files[name] = &fstest.MapFile{Data: data}
}

// fixtureRegistry publishes element/button, component/card, and page/optional
// behind a full profile, mirroring the fixtures the engine tests use.
func fixtureRegistry(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	putJSON(t, files, "registry.json", map[string]any{
		"schema": 2, "namespace": "ggg", "canonical_module": "github.com/gogogadget/gogogadget",
		"includes": []string{
			"registry/elements.json", "registry/components.json", "registry/pages.json",
			"registry/workflows.json", "registry/systems.json", "registry/profiles.json",
		},
	})
	putJSON(t, files, "registry/elements.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogElement, Items: []string{"registry/modules/element/button/module.json"}})
	putJSON(t, files, "registry/components.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogComponent, Items: []string{"registry/modules/component/card/module.json"}})
	putJSON(t, files, "registry/pages.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogPage, Items: []string{"registry/modules/page/optional/module.json"}})
	putJSON(t, files, "registry/workflows.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogWorkflow, Items: []string{}})
	putJSON(t, files, "registry/systems.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogSystem, Items: []string{}})
	putJSON(t, files, "registry/profiles.json", modkit.CatalogIndex{Schema: 2, Kind: modkit.CatalogProfile, Items: []string{"registry/profiles/full.json"}})

	buttonContent := []byte("package button\n\nconst ButtonVersion = 1\n")
	button := baseModule("ggg/element/button", "element", "button")
	button.Files = []modkit.ManifestFile{{
		Source: "registry/modules/element/button/button.go", Target: "internal/modules/button.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(buttonContent), RewriteModule: true, Contract: true,
	}}
	putJSON(t, files, "registry/modules/element/button/module.json", modkit.ModuleDocument{Schema: 2, Module: button})
	files[button.Files[0].Source] = &fstest.MapFile{Data: buttonContent}

	cardContent := []byte("package ui\n\nimport \"github.com/gogogadget/gogogadget/internal/modules/button\"\n\nconst CardUsesButton = button.ButtonVersion\n")
	card := baseModule("ggg/component/card", "component", "card")
	card.Requires = []modkit.Requirement{{ID: "ggg/element/button", Contract: modkit.ContractBounds{Min: 1, Max: 1}}}
	card.Files = []modkit.ManifestFile{{
		Source: "registry/modules/component/card/card.go", Target: "internal/web/templates/ui/card.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(cardContent), RewriteModule: true, Contract: true,
	}}
	putJSON(t, files, "registry/modules/component/card/module.json", modkit.ModuleDocument{Schema: 2, Module: card})
	files[card.Files[0].Source] = &fstest.MapFile{Data: cardContent}

	optionalContent := []byte("package optional\n\nconst Version = 1\n")
	optional := baseModule("ggg/page/optional", "page", "optional")
	optional.Files = []modkit.ManifestFile{{
		Source: "registry/modules/page/optional/optional.go", Target: "internal/modules/optional.go",
		Class: modkit.FileClassGo, SHA256: sha256Hex(optionalContent), Contract: true,
	}}
	// The page module carries one immutable migration so that removing it
	// leaves a TOMBSTONE rather than dropping the row: a lock record with
	// reason "removed", no files, and the ledger retained forever. That row
	// class is the one every verb that resolves an operand against the lock
	// has to agree about, and the documented-loop test walks it.
	optionalMigration := []byte("-- optional forward\nSELECT 1;\n")
	optionalMigrationSource := "registry/modules/page/optional/migrations/optional-forward.sql"
	optional.Migrations = []modkit.ManifestMigration{{
		ID: "optional-forward", Kind: modkit.MigrationImmutable,
		Source: optionalMigrationSource, SHA256: sha256Hex(optionalMigration),
	}}
	files[optionalMigrationSource] = &fstest.MapFile{Data: optionalMigration}
	putJSON(t, files, "registry/modules/page/optional/module.json", modkit.ModuleDocument{Schema: 2, Module: optional})
	files[optional.Files[0].Source] = &fstest.MapFile{Data: optionalContent}

	putJSON(t, files, "registry/profiles/full.json", map[string]any{
		"schema": 2,
		"profile": map[string]any{
			"id": "ggg/profile/full", "kind": "profile", "name": "full", "revision": 1, "contract": 1,
			"title": "Full", "description": "Every fixture module.",
			"members":                 []string{"ggg/component/card", "ggg/element/button", "ggg/page/optional"},
			"required_provider_slots": []string{}, "provider_defaults": map[string]any{}, "default_deployment": "",
		},
	})
	return files
}

func baseModule(id, kind, name string) modkit.Manifest {
	return modkit.Manifest{
		ID: id, Kind: modkit.ModuleKind(kind), Name: name, Revision: 1, Contract: 1,
		Title: name, Description: "Fixture " + name + ".",
		Requires:    []modkit.Requirement{},
		Claims:      modkit.NamespaceClaims{Packages: []string{}},
		Runtime:     modkit.RuntimeContributions{},
		Migrations:  []modkit.ManifestMigration{},
		Environment: []modkit.EnvironmentVariable{},
		Docs:        []modkit.DocumentationRef{},
		Tests:       modkit.TestMetadata{},
		Data:        []modkit.DataDeclaration{},
		Dependencies: modkit.Dependencies{
			Go: []modkit.GoDependency{}, Tools: []modkit.ToolArtifact{}, Containers: []modkit.ContainerDependency{},
		},
		RemovalPolicy: "free",
	}
}

// refSource resolves the fixture snapshots without a network.
type refSource struct {
	snapshots map[string]modkit.Snapshot
}

func (s refSource) Resolve(_ context.Context, registry modkit.ProjectRegistry) (modkit.Snapshot, error) {
	ref := registry.Ref
	if ref == "" {
		ref = "main"
	}
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return modkit.Snapshot{}, fmt.Errorf("unknown test ref %q", ref)
	}
	return snapshot, nil
}

// cliProject returns a bare project root plus an offline engine over the
// fixture registry.
func cliProject(t *testing.T) (string, *modkit.Engine) {
	t.Helper()
	fs := fixtureRegistry(t)
	source := refSource{snapshots: map[string]modkit.Snapshot{
		"main":      {Commit: testCommitA, FS: fs},
		testCommitA: {Commit: testCommitA, FS: fs},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	return root, modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})
}

func cloneFixtureFS(source fstest.MapFS) fstest.MapFS {
	clone := make(fstest.MapFS, len(source))
	for name, file := range source {
		copied := *file
		copied.Data = append([]byte(nil), file.Data...)
		clone[name] = &copied
	}
	return clone
}

func mutateFixtureModule(t *testing.T, files fstest.MapFS, manifestPath string, mutate func(*modkit.Manifest)) {
	t.Helper()
	file, ok := files[manifestPath]
	if !ok {
		t.Fatalf("fixture has no manifest at %s", manifestPath)
	}
	var document modkit.ModuleDocument
	if err := json.Unmarshal(file.Data, &document); err != nil {
		t.Fatal(err)
	}
	mutate(&document.Module)
	putJSON(t, files, manifestPath, document)
}

// conflictFixture is a project one `ggg update` away from a staged conflict:
// the installed file has a local edit and the registry's `v2` ref carries a
// different upstream for the same path.
type conflictFixture struct {
	root   string
	source modkit.Source
	engine *modkit.Engine
	// target is the conflicted path; local and upstream are the two sets of
	// bytes that collide over it.
	target   string
	local    []byte
	upstream []byte
}

// cliConflictSetup installs from one snapshot and edits the installed file,
// stopping before the update. Splitting it out is what lets the rollback gate
// drive the same update through an engine whose generation fails, which is the
// only way to observe a transaction that has already written the staged
// artifacts and must put the tree back.
func cliConflictSetup(t *testing.T) conflictFixture {
	t.Helper()
	first := fixtureRegistry(t)
	second := cloneFixtureFS(first)
	buttonV2 := []byte("package button\n\nconst ButtonVersion = 2\n")
	second["registry/modules/element/button/button.go"].Data = buttonV2
	mutateFixtureModule(t, second, "registry/modules/element/button/module.json", func(module *modkit.Manifest) {
		module.Revision = 2
		module.Files[0].SHA256 = sha256Hex(buttonV2)
	})
	source := refSource{snapshots: map[string]modkit.Snapshot{
		"main": {Commit: testCommitA, FS: first}, testCommitA: {Commit: testCommitA, FS: first},
		"v2": {Commit: testCommitB, FS: second}, testCommitB: {Commit: testCommitB, FS: second},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	engine := modkit.New(modkit.Options{Source: source, Generator: modkit.RegistryGenerator{}})

	// The intent is written directly rather than through `init --adopt`,
	// which pins an injected engine to a directory registry — and a directory
	// registry has no ref to advance, so there is no way to move upstream
	// under a local edit.
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema: 2,
		Registries: []modkit.ProjectRegistry{{
			Namespace: "ggg", Source: "github",
			Repository: "local/registry", Ref: "main", PublicKey: testKeyA,
		}},
		Modules: []string{"ggg/element/button"}, Exclude: []string{},
		Providers: map[string]modkit.ProviderSelections{}, Deployment: "",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	const target = "internal/modules/button.go"
	local := []byte("package button\n\nconst ButtonVersion = 1 // edited locally\n")
	writeTestFile(t, root, target, local)
	return conflictFixture{
		root: root, source: source, engine: engine,
		target: target, local: local, upstream: buttonV2,
	}
}

// stagedConflict is a project with one STAGED conflict, which is the only
// state `ggg resolve` runs in. Everything the resolve path touches — a local
// edit, an upstream that moved under it, a candidate staged rather than
// applied — is real: the fixture reaches the state the way an operator does,
// then ASSERTS the staging instead of performing it.
//
// The assertion used to be a write. `Plan.Staged` had no production writer, so
// this fixture put the candidate and the diff on disk itself and every resolve
// test passed over bytes production never produced — while `ggg resolve`
// refused every real conflict in every mode. A fixture that supplies what a
// production path owes is exactly how a green suite says nothing about the
// command.
type stagedConflict struct {
	conflictFixture
	conflict modkit.PendingConflict
	// updateOut and updateErr are the update's own streams and updateMessage
	// is the coded error it returned, so a gate can read exactly what the
	// operator is handed at exit 4.
	updateOut     string
	updateErr     string
	updateMessage string
}

func cliConflictProject(t *testing.T) stagedConflict {
	t.Helper()
	fixture := cliConflictSetup(t)
	out, errOut, updateErr := runApp(t, fixture.root, fixture.engine, "update", "--registry", "ggg", "--ref", "v2")
	if updateErr == nil || exitOf(t, updateErr) != 4 {
		t.Fatalf("update over a locally modified file = %v, want exit 4 with a staged conflict\n%s%s", updateErr, out, errOut)
	}

	conflict := pendingConflict(t, fixture.root, fixture.target)
	if got := sha256Hex(fixture.upstream); got != conflict.CandidateSHA256 {
		t.Fatalf("candidate digest = %q, want the recorded %q", got, conflict.CandidateSHA256)
	}
	if got := sha256Hex(readTestFile(t, fixture.root, conflict.CandidatePath)); got != conflict.CandidateSHA256 {
		t.Fatalf("staged candidate on disk digests %q, want the recorded %q", got, conflict.CandidateSHA256)
	}
	if len(readTestFile(t, fixture.root, conflict.DiffPath)) == 0 {
		t.Fatalf("update staged an empty diff at %s", conflict.DiffPath)
	}
	return stagedConflict{
		conflictFixture: fixture, conflict: conflict,
		updateOut: out, updateErr: errOut, updateMessage: updateErr.Error(),
	}
}

// pendingConflict is the single conflict the lock records for target. More
// than one, or none, is a broken fixture rather than a case to tolerate.
func pendingConflict(t *testing.T, root, target string) modkit.PendingConflict {
	t.Helper()
	lock, err := modkit.ParseLock(readTestFile(t, root, modkit.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	found := make([]modkit.PendingConflict, 0, 1)
	for _, module := range lock.Modules {
		if module.Pending == nil {
			continue
		}
		for _, conflict := range module.Pending.Conflicts {
			if conflict.Path == target {
				found = append(found, conflict)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("the lock records %d pending conflict(s) for %s, want exactly 1", len(found), target)
	}
	return found[0]
}

// exitOf extracts the exit code a CLI error carries.
func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var coder interface{ ExitCode() int }
	if !asExitCoder(err, &coder) {
		t.Fatalf("error %v carries no exit code", err)
	}
	return coder.ExitCode()
}

func asExitCoder(err error, target *interface{ ExitCode() int }) bool {
	for err != nil {
		if coder, ok := err.(interface{ ExitCode() int }); ok {
			*target = coder
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// runApp invokes the App with buffers, mirroring a non-TTY invocation.
func runApp(t *testing.T, root string, engine *modkit.Engine, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytesBuffer
	app := App{Out: &out, Err: &errOut, Root: root, Engine: engine, Version: "v1.2.3"}
	err := app.Run(context.Background(), args)
	return out.String(), errOut.String(), err
}

type bytesBuffer struct{ data []byte }

func (b *bytesBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}
func (b *bytesBuffer) String() string { return string(b.data) }
