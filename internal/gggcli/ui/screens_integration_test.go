package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gogogadget/gogogadget/internal/gggcli"
	"github.com/gogogadget/gogogadget/internal/modkit"
)

// stubSource resolves one fixture snapshot without a network.
type stubSource struct{ snapshot modkit.Snapshot }

func (s stubSource) Resolve(_ context.Context, _ modkit.ProjectRegistry) (modkit.Snapshot, error) {
	return s.snapshot, nil
}

// controllerProject builds a real Controller over a fixture registry plus a
// project intent, so the screens render rows the same way a live console
// does — through Execute and Preview, never a second engine.
func controllerProject(t *testing.T) (string, *gggcli.Controller) {
	t.Helper()
	// The fixture registry publishes one real component with a payload whose
	// digest matches, so the Catalog screen renders real rows rather than an
	// empty state.
	cardContent := []byte("package card\n\nconst CardVersion = 1\n")
	cardDoc := `{
		"schema": 2,
		"module": {
			"id": "ggg/component/card", "kind": "component", "name": "card",
			"revision": 1, "contract": 1,
			"title": "Card", "description": "A card component.",
			"requires": [],
			"files": [{
				"source": "registry/modules/component/card/card.go",
				"target": "internal/web/templates/ui/card.go",
				"class": "go", "sha256": "` + sha256Hex(cardContent) + `",
				"rewrite_module": true, "contract": true
			}],
			"claims": {}, "runtime": {}, "migrations": [], "environment": [],
			"docs": [], "tests": {}, "data": [],
			"dependencies": {"go": [], "tools": [], "containers": []},
			"removal_policy": "free"
		}
	}`
	files := fstest.MapFS{}
	files["registry.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":["registry/elements.json","registry/components.json","registry/pages.json","registry/workflows.json","registry/systems.json","registry/profiles.json"]}`)}
	files["registry/elements.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"element","items":[]}`)}
	files["registry/components.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"component","items":["registry/modules/component/card/module.json"]}`)}
	files["registry/pages.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"page","items":[]}`)}
	files["registry/workflows.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"workflow","items":[]}`)}
	files["registry/systems.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"system","items":[]}`)}
	files["registry/profiles.json"] = &fstest.MapFile{Data: []byte(`{"schema":2,"kind":"profile","items":[]}`)}
	files["registry/modules/component/card/module.json"] = &fstest.MapFile{Data: []byte(cardDoc)}
	files["registry/modules/component/card/card.go"] = &fstest.MapFile{Data: cardContent}

	const commit = "0123456789abcdef0123456789abcdef01234567"
	root := t.TempDir()
	write := func(name string, data string) {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/acme/app\n\ngo 1.26.6\n")
	write("gogogadget.json", `{"schema":2,"registries":[{"namespace":"ggg","source":"github","repository":"local/registry","ref":"main","public_key":"A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}],"modules":[],"exclude":[],"providers":{},"deployment":""}`)

	engine := modkit.New(modkit.Options{
		Source:    stubSource{snapshot: modkit.Snapshot{Commit: commit, FS: files}},
		Generator: modkit.RegistryGenerator{},
	})
	return root, gggcli.NewController(gggcli.ControllerOptions{Root: root, Version: "test", Engine: engine})
}

// sha256Hex hashes the fixture payload the way the registry does.
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// The Catalog screen loads its rows from the controller when entered, and
// renders the real module the registry publishes — never an injected row set.
func TestCatalogScreenLoadsRowsFromController(t *testing.T) {
	root, controller := controllerProject(t)
	cc := gggcli.CommandContext{Controller: controller, Interactive: false}
	m := newModel(context.Background(), cc)
	m.cc.Out = &stringsWriter{}
	m.push(screenCatalog)
	if len(m.catalog) != 1 || m.catalog[0].id != "ggg/component/card" || m.catalog[0].state != "available" {
		t.Fatalf("catalog rows = %#v, want the published card available", m.catalog)
	}
	_ = root

	view := renderView(m)
	if !containsAll(view, "Catalog", "ggg/component/card", "available", "Card") {
		t.Fatalf("catalog view missing real rows: %q", view)
	}
}

// The Providers screen renders one row per declared slot from the committed
// intent file.
func TestProvidersScreenRendersSlotRows(t *testing.T) {
	root, controller := controllerProject(t)
	intent := `{"schema":2,"registries":[{"namespace":"ggg","source":"github","repository":"local/registry","ref":"main","public_key":"A6EHv/POEL4dcN0Y50vAmWfk1jCbpQ1fHdyGZBJVMbg="}],"modules":[],"exclude":[],"providers":{"ggg/mail":{"development":{"adapter":"ggg/system/mail-dev","target":"filesystem"},"test":{"adapter":"ggg/system/mail-dev","target":"filesystem"},"production":{"adapter":"ggg/system/mail-resend","target":"resend"}}},"deployment":""}`
	if err := os.WriteFile(filepath.Join(root, "gogogadget.json"), []byte(intent), 0o644); err != nil {
		t.Fatal(err)
	}
	cc := gggcli.CommandContext{Controller: controller}
	m := newModel(context.Background(), cc)
	m.push(screenProviders)
	if len(m.providers) != 1 || m.providers[0].slot != "ggg/mail" {
		t.Fatalf("providers = %#v, want one ggg/mail row", m.providers)
	}
	if m.providers[0].development != "ggg/system/mail-dev@filesystem" || m.providers[0].production != "ggg/system/mail-resend@resend" {
		t.Fatalf("provider row = %#v", m.providers[0])
	}
	view := renderView(m)
	if !containsAll(view, "ggg/mail", "ggg/system/mail-dev@filesystem", "ggg/system/mail-resend@resend") {
		t.Fatalf("providers view missing rows: %q", view)
	}
}

// The Plan screen previews a sync through Preview and reports the zero-change
// state without writing anything.
func TestPlanScreenPreviewsWithoutWriting(t *testing.T) {
	root, controller := controllerProject(t)
	before := listFiles(t, root)
	cc := gggcli.CommandContext{Controller: controller}
	m := newModel(context.Background(), cc)
	m.push(screenPlan)
	// A project with no lock yet plans to create it; the row must be visible
	// and the tree untouched.
	if len(m.plan) == 0 {
		t.Fatal("expected the pending lock creation in the plan")
	}
	view := renderView(m)
	if !containsAll(view, "Plan", "gogogadget.lock.json") {
		t.Fatalf("plan view = %q", view)
	}
	if after := listFiles(t, root); !equalSlices(before, after) {
		t.Fatal("previewing the plan wrote to the tree")
	}
}

type stringsWriter struct{ b []byte }

func (w *stringsWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
