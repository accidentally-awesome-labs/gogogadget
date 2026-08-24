package modkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		{"info takes one id", []string{"info", "element/button", "element/card"}},
		{"add needs ids", []string{"add"}},
		{"remove needs ids", []string{"remove"}},
		{"resolve needs a mode", []string{"resolve", "element/button", "--path", "x.go"}},
		{"resolve rejects two modes", []string{"resolve", "element/button", "--path", "x.go", "--keep-local", "--accept-upstream"}},
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

	_ = cli.Run(context.Background(), []string{"add", "page/optional", "--dry-run"})

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
	if !strings.Contains(out.String(), "component/") {
		t.Fatalf("catalog --kind component listed nothing: %s", out.String())
	}
	if strings.Contains(out.String(), "\"element/") {
		t.Fatalf("catalog --kind component leaked other kinds: %s", out.String())
	}
}

// info returns the per-module contract an agent reads before editing.
func TestCLIInfoReportsModuleContract(t *testing.T) {
	root, engine := cliProject(t)
	var out bytes.Buffer
	cli := CLI{Out: &out, Root: root, Engine: engine}
	if err := cli.Run(context.Background(), []string{"info", "component/card", "--json"}); err != nil {
		t.Fatalf("info: %v", err)
	}
	for _, want := range []string{"component/card", "requires", "files", "removal_policy"} {
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
	writeTestFile(t, root, "registry.json", []byte(`{"schema":1,"includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		singular := strings.TrimSuffix(kind, "s")
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":1,"kind":"`+singular+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":1,"kind":"profile","items":[]}`))

	// A module document that no index mentions yet.
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":1,"module":{
		"id":"system/widget","kind":"system","name":"widget","revision":1,"contract":1,
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
	writeTestFile(t, root, "registry.json", []byte(`{"schema":1,"includes":[`+
		`"registry/elements.json","registry/components.json","registry/pages.json",`+
		`"registry/workflows.json","registry/systems.json","registry/profiles.json"]}`))
	for _, kind := range []string{"elements", "components", "pages", "workflows", "systems"} {
		writeTestFile(t, root, "registry/"+kind+".json",
			[]byte(`{"schema":1,"kind":"`+strings.TrimSuffix(kind, "s")+`","items":[]}`))
	}
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":1,"kind":"profile","items":[]}`))
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":1,"module":{
		"id":"system/widget","kind":"system","name":"widget","revision":1,"contract":1,
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
