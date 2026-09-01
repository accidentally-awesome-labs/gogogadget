package gggcli

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// selfHostTree writes a minimal schema-2 self-hosting registry tree.
func selfHostTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"namespace":"ggg","canonical_module":"github.com/gogogadget/gogogadget","includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":2,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":2,"kind":"profile","items":[]}`))
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],
		"files":[{"source":"internal/widget/widget.go","target":"internal/widget/widget.go",
		          "class":"go","sha256":"`+sha256Hex([]byte("package widget\n\nconst Version = 1\n"))+`","rewrite_module":true,"contract":true}],
		"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Version = 1\n"))
	return root
}

// `registry build` must discover module documents by scanning the registry
// tree; deriving the indexes from the indexes it writes would make a newly
// authored module permanently invisible.
func TestRegistryBuildDiscoversNewModuleDocuments(t *testing.T) {
	root := selfHostTree(t)
	if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(root, "registry", "systems.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "registry/modules/system/widget/module.json") {
		t.Fatalf("build did not discover the new module document:\n%s", index)
	}
	if _, _, err := runApp(t, root, nil, "registry", "validate"); err != nil {
		t.Fatalf("registry validate after build: %v", err)
	}
}

// Editing a module's own source stales its manifest; `registry build` is the
// authoring step that refreshes payload digests.
func TestRegistryBuildRefreshesPayloadDigests(t *testing.T) {
	root := selfHostTree(t)
	if _, _, err := runApp(t, root, nil, "registry", "validate"); err != nil {
		t.Fatalf("validate before edit: %v", err)
	}
	edited := []byte("package widget\n\nconst Version = 2\n")
	writeTestFile(t, root, "internal/widget/widget.go", edited)
	if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "registry", "modules", "system", "widget", "module.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), sha256Hex(edited)) {
		t.Fatalf("build did not refresh the payload digest:\n%s", data)
	}
	stale := sha256Hex([]byte("package widget\n\nconst Version = 1\n"))
	if strings.Contains(string(data), stale) {
		t.Fatalf("build left the stale digest behind:\n%s", data)
	}
	if _, _, err := runApp(t, root, nil, "registry", "validate"); err != nil {
		t.Fatalf("validate after build: %v", err)
	}
}

// An agent asked to change a component needs to know where to look at it and
// the exact commands that verify it. Both are derived rather than stored.
func TestSurfaceLinksAndVerificationCommandsAreDerived(t *testing.T) {
	m := modkit.Manifest{
		ID: "ggg/component/confirm-action",
		Runtime: modkit.RuntimeContributions{
			UI:        []modkit.UIContribution{{Name: "confirm-action", Family: "overlays"}},
			Scenarios: []modkit.ScenarioContribution{{Slug: "billing"}},
			Routes: []modkit.RouteContribution{
				{Method: http.MethodGet, Pattern: "/pricing"},
				{Method: http.MethodPost, Pattern: "/pricing/checkout"},
				{Method: http.MethodGet, Pattern: "/projects/{id}"},
			},
		},
		Tests: modkit.TestMetadata{
			GoPackages: []string{"internal/web/templates/ui"},
			E2E:        []string{"keyboard.spec.ts"},
			Visual:     []string{"gallery"},
		},
	}

	links := surfaceLinks(m)
	assert.Equal(t, []string{"/dev/gallery/overlays", "/dev/gallery/overlays/confirm-action"}, links["gallery"])
	assert.Equal(t, []string{"/dev/scenarios/billing"}, links["scenario"])
	assert.Equal(t, []string{"/pricing"}, links["route"],
		"a POST is an endpoint, not a place to look, and a pattern with a parameter is not a URL")

	commands := verificationCommands(m)
	assert.Contains(t, commands, "go test -count=1 ./internal/web/templates/ui")
	assert.Contains(t, commands, "cd e2e && npx playwright test keyboard.spec.ts --reporter=line")
	assert.Contains(t, commands, "./scripts/visual.sh",
		"visual baselines only match inside the pinned container, so the command must not look like a plain test run")
}

// A module with nothing to look at reports empty rather than absent.
func TestSurfaceLinksAreEmptyNotMissing(t *testing.T) {
	links := surfaceLinks(modkit.Manifest{ID: "ggg/system/observability"})
	assert.NotNil(t, links)
	assert.Empty(t, links)
	assert.Empty(t, verificationCommands(modkit.Manifest{ID: "ggg/system/observability"}))
}

// The CLI must actually carry both, or the derivation is unreachable.
func TestCLIInfoCarriesLinksAndVerify(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "info", "ggg/component/card", "--json")
	require.NoError(t, err)
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(out), &payload))
	for _, key := range []string{"links", "verify", "state", "module"} {
		require.Contains(t, payload, key, "info payload has no %q key: %s", key, out)
	}
}

// A generated target has no base digest, so it never appears in a change
// report; whether generated output is stale is `sync --check`'s question.
func TestDiffIgnoresGeneratedTargets(t *testing.T) {
	baseDigest := sha256Hex([]byte("package widget\n"))
	manifest := baseModule("ggg/system/widget", "system", "widget")
	manifest.Files = []modkit.ManifestFile{
		{Source: "registry/modules/system/widget/widget.go", Target: "internal/widget/widget.go", Class: modkit.FileClassGo, SHA256: baseDigest},
		{Source: "registry/modules/system/widget/widget.css", Target: "static/widget.css", Class: modkit.FileClassGenerated},
	}
	lock := modkit.Lock{
		Schema: 2, RegistryCommit: testCommitA,
		Order: []string{manifest.ID},
		Modules: []modkit.LockedModule{{
			ID: manifest.ID, Revision: 1, Contract: 1, SourceCommit: testCommitA,
			Reason: "explicit", RequiredBy: []string{}, Manifest: manifest,
			Files: []modkit.LockedFile{
				{Path: "internal/widget/widget.go", Source: manifest.Files[0].Source, BaseSHA256: baseDigest, LocalSHA256: baseDigest, State: modkit.FileClean},
				{Path: "static/widget.css", Source: manifest.Files[1].Source, State: modkit.FileGenerated},
			},
			Migrations: []modkit.LockedMigration{},
		}},
	}
	data, err := modkit.MarshalLock(lock)
	require.NoError(t, err)

	root := t.TempDir()
	writeTestFile(t, root, modkit.LockFileName, data)
	// A locally edited source file is a real change; the generated target
	// must stay out of the report because it has no base to compare against.
	writeTestFile(t, root, "internal/widget/widget.go", []byte("package widget\n\nconst Edited = true\n"))

	controller := NewController(ControllerOptions{Root: root})
	entries, commit, err := controller.collectDiff(nil, false)
	require.NoError(t, err)
	assert.Equal(t, testCommitA, commit)
	require.Len(t, entries, 1, "only the edited source file may appear: %#v", entries)
	assert.Equal(t, "internal/widget/widget.go", entries[0].Path)
	assert.Equal(t, "modified", entries[0].State)
}

var _ = context.Background
