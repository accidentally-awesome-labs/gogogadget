package modkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliProject returns a bare project root (go.mod only, no intent file) plus an
// engine over the offline fixture registry, so every CLI test runs offline and
// `init` has something real to create.
func cliProject(t *testing.T) (string, *Engine) {
	t.Helper()
	first, _ := removalRegistries(t)
	source := refSource{snapshots: map[string]Snapshot{
		"main":      {Commit: testCommitA, FS: first},
		testCommitA: {Commit: testCommitA, FS: first},
	}}
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	return root, New(Options{Source: source, Generator: RegistryGenerator{}})
}

// exitOf extracts the exit code a CLI error carries. Every CLI error must carry
// one: an error without an exit code would silently become exit 1.
func exitOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("error %v carries no exit code", err)
	}
	return coder.ExitCode()
}

// Usage failures are exit 2 and must never be confused with a runtime failure.
func TestCLIRejectsUnknownAndMalformedCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"no command", nil},
		{"unknown command", []string{"frobnicate"}},
		{"version takes no args", []string{"version", "extra"}},
		{"info needs an id", []string{"info"}},
		{"info takes one id", []string{"info", "ggg/element/button", "ggg/element/card"}},
		{"add needs ids", []string{"add"}},
		{"remove needs ids", []string{"remove"}},
		{"resolve needs a mode", []string{"resolve", "ggg/element/button", "--path", "x.go"}},
		{"resolve rejects two modes", []string{"resolve", "ggg/element/button", "--path", "x.go", "--keep-local", "--accept-upstream"}},
		{"registry needs a subcommand", []string{"registry"}},
		{"registry rejects unknown subcommand", []string{"registry", "demolish"}},
		{"unknown flag", []string{"sync", "--nope"}},
		{"bad id", []string{"info", "not-an-id"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			cli := CLI{Out: &out, Err: &errOut, Root: t.TempDir()}
			err := cli.Run(context.Background(), tc.args)
			if err == nil {
				t.Fatalf("Run(%v) = nil error, want usage failure", tc.args)
			}
			if got := exitOf(t, err); got != 2 {
				t.Fatalf("Run(%v) exit = %d, want 2", tc.args, got)
			}
		})
	}
}

// version is the one command that works without a project.
func TestCLIVersionNeedsNoProject(t *testing.T) {
	var out bytes.Buffer
	cli := CLI{Out: &out, Version: "v1.2.3", Root: t.TempDir()}
	if err := cli.Run(context.Background(), []string{"version"}); err != nil {
		t.Fatalf("version: %v", err)
	}
	if got := out.String(); got != "ggg v1.2.3\n" {
		t.Fatalf("version output = %q", got)
	}
}

// Every --json command emits exactly the declared envelope. A missing key is a
// broken contract for every machine consumer, so assert the full key set.
func TestCLIJSONEnvelopeMatchesDeclaredSchema(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}

	if err := cli.Run(context.Background(), []string{"init", "--adopt", "--json"}); err != nil {
		t.Fatalf("init --adopt --json: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, out.String())
	}
	want := []string{
		"ok", "command", "run_id", "registry_commit",
		"resolved", "changes", "generated", "conflicts", "diagnostics", "exit",
	}
	for _, key := range want {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing %q: %s", key, out.String())
		}
	}
	if len(envelope) != len(want) {
		t.Fatalf("envelope has %d keys, want exactly %d: %s", len(envelope), len(want), out.String())
	}
	if envelope["ok"] != true {
		t.Fatalf("ok = %v, want true", envelope["ok"])
	}
	if envelope["command"] != "init" {
		t.Fatalf("command = %v, want init", envelope["command"])
	}
	if envelope["exit"] != float64(0) {
		t.Fatalf("exit = %v, want 0", envelope["exit"])
	}
	if envelope["run_id"] == "" {
		t.Fatal("run_id is empty")
	}
}

// init --adopt must produce a real project and lock on disk; a JSON envelope
// that reports success without writing them would be a lie.
func TestCLIInitAdoptWritesProjectAndLock(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}

	if err := cli.Run(context.Background(), []string{"init", "--adopt"}); err != nil {
		t.Fatalf("init --adopt: %v", err)
	}
	for _, name := range []string{"gogogadget.json", "gogogadget.lock.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("init did not write %s: %v", name, err)
		}
	}
}

// sync --check is the drift gate: clean after a sync, and non-zero the moment a
// generated output is edited. Exit 4 is the declared drift code.
func TestCLISyncCheckDetectsGeneratedDrift(t *testing.T) {
	root, engine := cliProject(t)
	cli := CLI{Out: &bytes.Buffer{}, Root: root, Engine: engine}

	if err := cli.Run(context.Background(), []string{"init", "--adopt"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := cli.Run(context.Background(), []string{"sync", "--offline"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := cli.Run(context.Background(), []string{"sync", "--check", "--offline"}); err != nil {
		t.Fatalf("sync --check on a clean tree: %v", err)
	}
}

// dry-run must never touch the tree: the intent file is the thing add edits, so
// a dry-run add that rewrites it has already broken the contract.
func TestCLIDryRunLeavesIntentUntouched(t *testing.T) {
	root, engine := cliProject(t)
	cli := CLI{Out: &bytes.Buffer{}, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"init", "--adopt"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentPath := filepath.Join(root, "gogogadget.json")
	before, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatalf("read intent: %v", err)
	}

	_ = cli.Run(context.Background(), []string{"add", "ggg/page/optional", "--dry-run"})

	after, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatalf("read intent after dry-run: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("dry-run rewrote the intent file:\nbefore %s\nafter  %s", before, after)
	}
}

// doctor reports on an uninitialized project rather than crashing: it is the
// command an operator runs when things are already wrong.
func TestCLIDoctorReportsMissingProject(t *testing.T) {
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: t.TempDir(), Engine: mustCLIEngine(t)}
	err := cli.Run(context.Background(), []string{"doctor", "--json"})
	if err != nil {
		if got := exitOf(t, err); got != 3 {
			t.Fatalf("doctor on missing project exit = %d, want 3", got)
		}
	}
	if !strings.Contains(out.String(), "\"ok\"") {
		t.Fatalf("doctor emitted no envelope: %s", out.String())
	}
}

// catalog lists the registry without a project; --kind narrows it.
func TestCLICatalogListsRegistry(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"catalog", "--kind", "component", "--json"}); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !strings.Contains(out.String(), "ggg/component/") {
		t.Fatalf("catalog --kind component listed nothing: %s", out.String())
	}
	if strings.Contains(out.String(), "\"ggg/element/") {
		t.Fatalf("catalog --kind component leaked other kinds: %s", out.String())
	}
}

// info returns the per-module contract an agent reads before editing.
func TestCLIInfoReportsModuleContract(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"info", "ggg/component/card", "--json"}); err != nil {
		t.Fatalf("info: %v", err)
	}
	for _, want := range []string{"ggg/component/card", "requires", "files", "removal_policy"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("info output missing %q: %s", want, out.String())
		}
	}
}

func mustCLIEngine(t *testing.T) *Engine {
	t.Helper()
	_, engine, _ := installedRemovalProject(t)
	return engine
}

// `registry build` must discover module documents by scanning the registry tree.
// Deriving the indexes from the indexes it is supposed to write would make a
// newly authored module permanently invisible.
func TestCLIRegistryBuildDiscoversNewModuleDocuments(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		singular := strings.TrimSuffix(kind, "s")
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":2,"kind":"`+singular+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":2,"kind":"profile","items":[]}`))

	// A module document that no index mentions yet.
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],"files":[],
		"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"removal_policy":"free"}}`))

	cli := CLI{Out: &bytes.Buffer{}, Root: root}
	if err := cli.Run(context.Background(), []string{"registry", "build"}); err != nil {
		t.Fatalf("registry build: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(root, "registry", "systems.json"))
	if err != nil {
		t.Fatalf("read systems index: %v", err)
	}
	if !strings.Contains(string(index), "registry/modules/system/widget/module.json") {
		t.Fatalf("build did not discover the new module document:\n%s", index)
	}

	// The rebuilt index must load, proving build and validate agree.
	if err := cli.Run(context.Background(), []string{"registry", "validate"}); err != nil {
		t.Fatalf("registry validate after build: %v", err)
	}
}

// A tree that contains its own registry.json is a self-hosting registry: the
// upstream repository itself, and any derivative that vendors the catalog. It
// must resolve from its own tree, never the network, so `make check` works in a
// fresh clone with no credentials.
func TestCLIResolvesSelfHostedRegistryFromTree(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":2,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":2,"kind":"profile","items":[]}`))
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],"files":[],
		"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"removal_policy":"free"}}`))

	// No Engine injected: the CLI must build one that resolves locally.
	cli := CLI{Out: &bytes.Buffer{}, Root: root}
	if err := cli.Run(context.Background(), []string{"registry", "build"}); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	if err := cli.Run(context.Background(), []string{"init", "--adopt", "--offline"}); err != nil {
		t.Fatalf("init --adopt --offline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, LockFileName)); err != nil {
		t.Fatalf("self-hosted adopt wrote no lock: %v", err)
	}
}

// `sync --check` is the gate that proves a clone has no generated drift. It must
// render the aggregates and compare bytes: relying on planner changes alone
// misses generated output entirely, because the generator — not the planner —
// produces it.
func TestCLISyncCheckDetectsTamperedAndMissingGeneratedOutput(t *testing.T) {
	root, engine := cliProject(t)
	cli := CLI{Out: &bytes.Buffer{}, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"init", "--adopt"}); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := cli.Run(context.Background(), []string{"sync", "--check", "--offline"}); err != nil {
		t.Fatalf("check on a clean tree: %v", err)
	}

	generated := filepath.Join(root, "internal", "modules", "bootstrap_registry_gen.go")
	original, err := os.ReadFile(generated)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}

	t.Run("tampered bytes", func(t *testing.T) {
		if err := os.WriteFile(generated, append(original, []byte("\n// drift\n")...), 0o644); err != nil {
			t.Fatalf("tamper: %v", err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })

		err := cli.Run(context.Background(), []string{"sync", "--check", "--offline"})
		if err == nil {
			t.Fatal("check passed on tampered generated output")
		}
		if got := exitOf(t, err); got != 4 {
			t.Fatalf("tampered check exit = %d, want 4", got)
		}
	})

	t.Run("missing output", func(t *testing.T) {
		if err := os.Remove(generated); err != nil {
			t.Fatalf("remove: %v", err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })

		err := cli.Run(context.Background(), []string{"sync", "--check", "--offline"})
		if err == nil {
			t.Fatal("check passed with a missing generated output")
		}
		if got := exitOf(t, err); got != 4 {
			t.Fatalf("missing check exit = %d, want 4", got)
		}
	})
}

// Adoption of an existing product is: author the intent, then sync. A divergent
// pre-existing file must refuse with exit 3 and name the remedy; --claim adopts
// the operator's bytes untouched and records them as modified.
func TestCLISyncClaimsDivergentFileDuringAdoption(t *testing.T) {
	root, engine := cliProject(t)
	intent, err := MarshalProject(Project{
		Schema: 2,
		Registries: []ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: "core"}}, Providers: map[string]ProviderSelections{}, Deployment: "",
		Modules:  []string{"ggg/page/optional"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatalf("MarshalProject: %v", err)
	}
	writeTestFile(t, root, ProjectFileName, intent)

	local := []byte("package optional\n\n// mine\nconst Version = 99\n")
	writeTestFile(t, root, "internal/modules/optional.go", local)

	cli := CLI{Out: &bytes.Buffer{}, Root: root, Engine: engine}
	err = cli.Run(context.Background(), []string{"sync", "--offline"})
	if err == nil {
		t.Fatal("sync overwrote a divergent pre-existing file")
	}
	if got := exitOf(t, err); got != 3 {
		t.Fatalf("unclaimed adoption exit = %d, want 3", got)
	}

	// --check must refuse to carry a mutating claim.
	if err := cli.Run(context.Background(), []string{
		"sync", "--check", "--offline", "--claim", "internal/modules/optional.go",
	}); err == nil || exitOf(t, err) != 2 {
		t.Fatalf("sync --check --claim error = %v, want usage failure", err)
	}

	if err := cli.Run(context.Background(), []string{
		"sync", "--offline", "--claim", "internal/modules/optional.go",
	}); err != nil {
		t.Fatalf("sync --claim: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(root, "internal", "modules", "optional.go"))
	if err != nil {
		t.Fatalf("read claimed file: %v", err)
	}
	if !bytes.Equal(after, local) {
		t.Fatalf("claimed file was rewritten:\nwant %s\ngot  %s", local, after)
	}

	lockData, err := os.ReadFile(filepath.Join(root, LockFileName))
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	lock, err := ParseLock(lockData)
	if err != nil {
		t.Fatalf("ParseLock: %v", err)
	}
	found := false
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			if file.Path != "internal/modules/optional.go" {
				continue
			}
			found = true
			if file.State != FileModified {
				t.Fatalf("claimed file state = %q, want %q", file.State, FileModified)
			}
			if file.BaseSHA256 == file.LocalSHA256 {
				t.Fatal("base_sha256 equals local_sha256; upstream digest was not recorded")
			}
		}
	}
	if !found {
		t.Fatal("claimed file is absent from the lock")
	}
}

// In a self-hosting registry the module payload and the manifest digest live in
// the same tree, so editing a module's own source stales its manifest. Without a
// refresh the upstream author can never evolve a module: every sync refuses on a
// digest mismatch. `registry build` is that authoring step.
func TestCLIRegistryBuildRefreshesPayloadDigests(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	writeTestFile(t, root, "registry.json", []byte(`{"schema":2,"includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":2,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":2,"kind":"profile","items":[]}`))

	payload := []byte("package widget\n\nconst Version = 1\n")
	writeTestFile(t, root, "internal/widget/widget.go", payload)
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],
		"files":[{"source":"internal/widget/widget.go","target":"internal/widget/widget.go",
		          "class":"go","sha256":"`+sha256Hex(payload)+`","rewrite_module":true,"contract":true}],
		"claims":{},"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"removal_policy":"free"}}`))

	cli := CLI{Out: &bytes.Buffer{}, Root: root}
	if err := cli.Run(context.Background(), []string{"registry", "validate"}); err != nil {
		t.Fatalf("validate before edit: %v", err)
	}

	// The upstream author edits the module's own source.
	edited := []byte("package widget\n\nconst Version = 2\n")
	writeTestFile(t, root, "internal/widget/widget.go", edited)

	if err := cli.Run(context.Background(), []string{"registry", "build"}); err != nil {
		t.Fatalf("registry build: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "registry", "modules", "system", "widget", "module.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(data), sha256Hex(edited)) {
		t.Fatalf("build did not refresh the payload digest:\n%s", data)
	}
	if strings.Contains(string(data), sha256Hex(payload)) {
		t.Fatalf("build left the stale digest behind:\n%s", data)
	}
	if err := cli.Run(context.Background(), []string{"registry", "validate"}); err != nil {
		t.Fatalf("validate after build: %v", err)
	}
}

// An agent asked to change a component needs to know where to look at it and
// the exact commands that verify it. A package list is not a command - the
// reader still has to know this project runs `go test` by package, that e2e
// lives in a sibling directory with its own runner, and that visual baselines
// are regenerated inside a pinned container rather than asserted in place.
//
// Both are derived rather than stored: putting URLs in manifests would let them
// drift from the routes that serve them.
func TestSurfaceLinksAndVerificationCommandsAreDerived(t *testing.T) {
	m := Manifest{
		ID: "ggg/component/confirm-action",
		Runtime: RuntimeContributions{
			UI:        []UIContribution{{Name: "confirm-action", Family: "overlays"}},
			Scenarios: []ScenarioContribution{{Slug: "billing"}},
			Routes: []RouteContribution{
				{Method: http.MethodGet, Pattern: "/pricing"},
				{Method: http.MethodPost, Pattern: "/pricing/checkout"},
				{Method: http.MethodGet, Pattern: "/projects/{id}"},
			},
		},
		Tests: TestMetadata{
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

// A module with nothing to look at and nothing to run must report empty rather
// than absent keys, so a consumer can distinguish "no surface" from "field not
// implemented".
func TestSurfaceLinksAreEmptyNotMissing(t *testing.T) {
	links := surfaceLinks(Manifest{ID: "ggg/system/observability"})
	assert.NotNil(t, links)
	assert.Empty(t, links)
	assert.Empty(t, verificationCommands(Manifest{ID: "ggg/system/observability"}))
}

// The CLI must actually carry both, or the derivation is unreachable.
func TestCLIInfoCarriesLinksAndVerify(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"info", "ggg/component/card", "--json"}); err != nil {
		t.Fatalf("info: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode info payload: %v", err)
	}
	for _, key := range []string{"links", "verify", "state", "module"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("info payload has no %q key: %s", key, out.String())
		}
	}
}

// A generated target has no base digest, so comparing one against a base always
// reports "modified" - in every repository, forever. Three permanent phantom
// rows in `ggg diff` teach the reader that its output is noise, which is the one
// thing a change-report must not do. Whether generated output is stale is
// `sync --check`'s question: it re-renders and compares bytes.
func TestDiffIgnoresGeneratedTargets(t *testing.T) {
	entries := []DiffEntry{}
	lock := Lock{Modules: []LockedModule{{
		ID: "ggg/system/static",
		Files: []LockedFile{
			{Path: "static/app.css", State: FileGenerated, BaseSHA256: ""},
			{Path: "internal/thing.go", State: FileClean, BaseSHA256: "abc"},
		},
	}}}

	for _, module := range lock.Modules {
		for _, file := range module.Files {
			if file.State == FileGenerated {
				continue
			}
			entries = append(entries, DiffEntry{Module: module.ID, Path: file.Path})
		}
	}

	require.Len(t, entries, 1, "a generated payload must not appear in a change report")
	assert.Equal(t, "internal/thing.go", entries[0].Path)
}

// The manifest digest of a generated payload is written by `registry build` and
// read by nothing: readPlannedPayloads returns early on FileClassGenerated,
// before the check that raises "payload ... sha256 mismatch". Recording one
// rewrote manifests on every build for no consumer.
func TestRegistryBuildRecordsNoDigestForGeneratedPayloads(t *testing.T) {
	root := repoRootFromTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "registry", "modules", "system", "static", "module.json"))
	require.NoError(t, err)

	var document ModuleDocument
	require.NoError(t, decodeStrict(raw, &document))

	generated := 0
	for _, file := range document.Module.Files {
		if file.Class != FileClassGenerated {
			continue
		}
		generated++
		assert.Emptyf(t, file.SHA256,
			"%s is generated, so its digest is never verified; recording one is churn that rewrites this manifest on every build", file.Target)
	}
	require.Positive(t, generated, "ggg/system/static declares no generated payload, so this proves nothing")
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "..", "..")
}
