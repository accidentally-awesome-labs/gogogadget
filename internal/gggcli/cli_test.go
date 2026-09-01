package gggcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Usage failures are exit 2 and must never be confused with a runtime failure.
// The non-TTY no-arg invocation is the declared interactive_terminal_required
// usage failure.
func TestCLIRejectsUnknownAndMalformedCommands(t *testing.T) {
	cases := [][]string{
		nil,
		{"frobnicate"},
		{"version", "extra"},
		{"info"},
		{"info", "element/button", "element/card"},
		{"add"},
		{"remove"},
		{"resolve", "element/button", "--path", "x.go"},
		{"resolve", "element/button", "--path", "x.go", "--keep-local", "--accept-upstream"},
		{"registry"},
		{"registry", "demolish"},
		{"sync", "--nope"},
		{"info", "not-an-id"},
		{"version", "--json"},
		{"cache", "prune", "--json"},
	}
	for _, args := range cases {
		_, _, err := runApp(t, t.TempDir(), nil, args...)
		if err == nil {
			t.Fatalf("Run(%v) = nil error, want usage failure", args)
		}
		if got := exitOf(t, err); got != 2 {
			t.Fatalf("Run(%v) exit = %d, want 2", args, got)
		}
	}
}

// version is the one command that works without a project.
func TestCLIVersionNeedsNoProject(t *testing.T) {
	out, _, err := runApp(t, t.TempDir(), nil, "version")
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if out != "ggg v1.2.3\n" {
		t.Fatalf("version output = %q", out)
	}
}

// Every --json command emits exactly the declared envelope. A missing key is a
// broken contract for every machine consumer.
func TestCLIJSONEnvelopeMatchesDeclaredSchema(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "init", "--adopt", "--json")
	if err != nil {
		t.Fatalf("init --adopt --json: %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("envelope is not JSON: %v\n%s", err, out)
	}
	want := []string{
		"ok", "command", "run_id", "registry_commit",
		"resolved", "changes", "generated", "conflicts", "diagnostics", "exit",
	}
	for _, key := range want {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing %q: %s", key, out)
		}
	}
	if len(envelope) != len(want) {
		t.Fatalf("envelope has %d keys, want exactly %d: %s", len(envelope), len(want), out)
	}
	if envelope["ok"] != true || envelope["command"] != "init" || envelope["exit"] != float64(0) {
		t.Fatalf("envelope = %#v", envelope)
	}
	if envelope["run_id"] == "" {
		t.Fatal("run_id is empty")
	}
}

// init --adopt must produce a real project and lock on disk.
func TestCLIInitAdoptWritesProjectAndLock(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init --adopt: %v", err)
	}
	for _, name := range []string{"gogogadget.json", "gogogadget.lock.json"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("init did not write %s: %v", name, err)
		}
	}
}

// sync --check is the drift gate: clean after a sync, exit 4 the moment a
// generated output is edited or missing.
func TestCLISyncCheckDetectsTamperedAndMissingGeneratedOutput(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline"); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--check", "--offline"); err != nil {
		t.Fatalf("check on a clean tree: %v", err)
	}

	generated := filepath.Join(root, "internal", "modules", "bootstrap_registry_gen.go")
	original, err := os.ReadFile(generated)
	if err != nil {
		t.Fatalf("read generated: %v", err)
	}
	t.Run("tampered bytes", func(t *testing.T) {
		if err := os.WriteFile(generated, append(original, []byte("\n// drift\n")...), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })
		_, _, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("tampered check = %v, want exit 4", err)
		}
	})
	t.Run("missing output", func(t *testing.T) {
		if err := os.Remove(generated); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.WriteFile(generated, original, 0o644) })
		_, _, err := runApp(t, root, engine, "sync", "--check", "--offline")
		if err == nil || exitOf(t, err) != 4 {
			t.Fatalf("missing check = %v, want exit 4", err)
		}
	})
}

// dry-run must never touch the tree: zero writes before apply.
func TestCLIDryRunLeavesIntentUntouched(t *testing.T) {
	root, engine := cliProject(t)
	if _, _, err := runApp(t, root, engine, "init", "--adopt"); err != nil {
		t.Fatalf("init: %v", err)
	}
	intentPath := filepath.Join(root, "gogogadget.json")
	before, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	// A dry-run add of a scoped id refuses cleanly; either way the tree is
	// untouched, which is the contract this test pins.
	_, _, _ = runApp(t, root, engine, "add", "page/optional", "--dry-run")
	after, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run rewrote the intent file")
	}
}

// doctor reports on an uninitialized project rather than crashing.
func TestCLIDoctorReportsMissingProject(t *testing.T) {
	_, engine := cliProject(t)
	// Doctor reports on an existing but uninitialized directory.
	out, _, err := runApp(t, t.TempDir(), engine, "doctor", "--json")
	if err != nil && exitOf(t, err) != 3 {
		t.Fatalf("doctor on missing project exit = %d, want 3", exitOf(t, err))
	}
	if !strings.Contains(out, "\"ok\"") {
		t.Fatalf("doctor emitted no envelope: %s", out)
	}
}

// catalog lists the registry without a project; --kind narrows it.
func TestCLICatalogListsRegistry(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "catalog", "--kind", "component", "--json")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if !strings.Contains(out, "component/") || strings.Contains(out, "\"element/") {
		t.Fatalf("catalog --kind component = %s", out)
	}
}

// info returns the per-module contract an agent reads before editing.
func TestCLIInfoReportsModuleContract(t *testing.T) {
	root, engine := cliProject(t)
	out, _, err := runApp(t, root, engine, "info", "ggg/component/card", "--json")
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	for _, want := range []string{"ggg/component/card", "requires", "files", "removal_policy", "links", "verify", "state", "module"} {
		if !strings.Contains(out, want) {
			t.Fatalf("info output missing %q: %s", want, out)
		}
	}
}

// The self-hosting resolution: a tree with its own registry.json resolves from
// its own tree, never the network.
func TestCLIResolvesSelfHostedRegistryFromTree(t *testing.T) {
	root := t.TempDir()
	fs := fixtureRegistry(t)
	for name, file := range fs {
		writeTestFile(t, root, name, file.Data)
	}
	writeTestFile(t, root, "go.mod", []byte("module example.com/acme/app\n\ngo 1.26.6\n"))
	if _, _, err := runApp(t, root, nil, "registry", "build"); err != nil {
		t.Fatalf("registry build: %v", err)
	}
	if _, _, err := runApp(t, root, nil, "init", "--adopt", "--offline"); err != nil {
		t.Fatalf("init --adopt --offline: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, modkit.LockFileName)); err != nil {
		t.Fatalf("self-hosted adopt wrote no lock: %v", err)
	}
}

// A divergent pre-existing file must refuse with exit 3; --claim adopts the
// operator's bytes untouched and records them as modified.
func TestCLISyncClaimsDivergentFileDuringAdoption(t *testing.T) {
	root, engine := cliProject(t)
	intent, err := modkit.MarshalProject(modkit.Project{
		Schema:     2,
		Registries: []modkit.ProjectRegistry{{Namespace: "ggg", Source: "github", Repository: "local/registry", Ref: "main", PublicKey: testKeyA}},
		Providers:  map[string]modkit.ProviderSelections{}, Deployment: "",
		Modules: []string{"ggg/page/optional"}, Exclude: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.ProjectFileName, intent)
	local := []byte("package optional\n\n// mine\nconst Version = 99\n")
	writeTestFile(t, root, "internal/modules/optional.go", local)

	_, _, err = runApp(t, root, engine, "sync", "--offline")
	if err == nil || exitOf(t, err) != 3 {
		t.Fatalf("unclaimed adoption = %v, want exit 3", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--check", "--offline", "--claim", "internal/modules/optional.go"); err == nil || exitOf(t, err) != 2 {
		t.Fatalf("sync --check --claim = %v, want usage failure", err)
	}
	if _, _, err := runApp(t, root, engine, "sync", "--offline", "--claim", "internal/modules/optional.go"); err != nil {
		t.Fatalf("sync --claim: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "internal", "modules", "optional.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, local) {
		t.Fatal("claimed file was rewritten")
	}
	lock, err := modkit.ParseLock([]byte(readFile(t, filepath.Join(root, modkit.LockFileName))))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, module := range lock.Modules {
		for _, file := range module.Files {
			if file.Path != "internal/modules/optional.go" {
				continue
			}
			found = true
			if file.State != modkit.FileModified {
				t.Fatalf("claimed file state = %q, want %q", file.State, modkit.FileModified)
			}
			if file.BaseSHA256 == file.LocalSHA256 {
				t.Fatal("base digest was not recorded")
			}
		}
	}
	if !found {
		t.Fatal("claimed file is absent from the lock")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// help and completions are derived from the command table, including the
// contributed ui command.
func TestHelpAndCompletionsDerivedFromTable(t *testing.T) {
	var out bytes.Buffer
	app := App{Out: &out, Contributed: []ContributedCommand{{Spec: CommandSpec{Name: "ui", Summary: "Open the console", Usage: "ggg ui"}}}}
	if err := app.Run(context.Background(), []string{"--help"}); err != nil {
		t.Fatalf("--help: %v", err)
	}
	for _, want := range []string{"version", "sync", "ui", "Open the console"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("help missing %q: %s", want, out.String())
		}
	}
	out.Reset()
	if err := app.Run(context.Background(), []string{"help", "sync"}); err != nil {
		t.Fatalf("help sync: %v", err)
	}
	if !strings.Contains(out.String(), "--check") {
		t.Fatalf("help sync missing flags: %s", out.String())
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out.Reset()
		if err := app.Run(context.Background(), []string{"completion", shell}); err != nil {
			t.Fatalf("completion %s: %v", shell, err)
		}
		if !strings.Contains(out.String(), "ggg") {
			t.Fatalf("completion %s is empty", shell)
		}
	}
}

// No-arg without a terminal is the declared usage failure, never a UI.
func TestNoArgNonTTYIsInteractiveTerminalRequired(t *testing.T) {
	_, _, err := runApp(t, t.TempDir(), nil)
	if err == nil || exitOf(t, err) != 2 {
		t.Fatalf("no-arg non-TTY = %v, want exit 2", err)
	}
	if !strings.Contains(err.Error(), "interactive_terminal_required") {
		t.Fatalf("no-arg non-TTY message = %v", err)
	}
}

// `ui` contributed on a non-TTY stream exits 2 with the same refusal. The
// handler is gggcli/ui.Run via a stub, because the nonvisual tests must not
// import the Charm stack.
func TestUICommandNonTTYRefuses(t *testing.T) {
	app := App{Out: &bytes.Buffer{}, Contributed: []ContributedCommand{{
		Spec: CommandSpec{Name: "ui", Summary: "console"},
		Handler: func(_ context.Context, cc CommandContext, _ []string) (Result, error) {
			if !cc.Interactive {
				return Result{}, usageError("interactive_terminal_required")
			}
			return Result{}, nil
		},
	}}}
	_, _, err := runAppWith(t, app, "ui")
	if err == nil || exitOf(t, err) != 2 {
		t.Fatalf("ui non-TTY = %v, want exit 2", err)
	}
}

// Cancelled after preview is the declared user_cancelled refusal (exit 3);
// cancelled before any plan is a clean exit 0.
func TestCancellationContract(t *testing.T) {
	if code := ExitCode(ErrCancelled); code != 0 {
		t.Fatalf("cancel before plan exit = %d, want 0", code)
	}
	err := cancelledAfterPreview("sync")
	if code := ExitCode(err); code != 3 {
		t.Fatalf("cancel after preview exit = %d, want 3", code)
	}
	var user UserCancelledError
	if !errors.As(err, &user) || user.Command != "sync" {
		t.Fatalf("error = %#v, want UserCancelledError{sync}", err)
	}
}

func runAppWith(t *testing.T, app App, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytesBuffer
	app.Out, app.Err = &out, &errOut
	err := app.Run(context.Background(), args)
	return out.String(), errOut.String(), err
}
