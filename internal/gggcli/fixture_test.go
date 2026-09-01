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
