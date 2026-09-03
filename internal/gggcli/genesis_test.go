package gggcli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// Genesis is a journalled transaction over a tree that has to compile. `ggg
// new` installs authored source and renders every registry aggregate, but the
// tool outputs — templ's *_templ.go, the sqlc package internal/db/module.go
// imports, and the static/app.css that static/embed_registry_gen.go names in a
// compile-time //go:embed pattern — are inputs to compilation that no manifest
// ships. A project missing them cannot even build the bin/ggg that `ggg setup`
// needs, so genesis produces them and proves the result builds before it
// reports success.

// recordingRunner records every task argv and fails the ones named in fail.
type recordingRunner struct {
	calls []string
	roots []string
	envs  []map[string]string
	fail  map[string]error
}

func (r *recordingRunner) Run(_ context.Context, root string, argv []string, env map[string]string) error {
	line := strings.Join(argv, " ")
	r.calls = append(r.calls, line)
	r.roots = append(r.roots, root)
	r.envs = append(r.envs, env)
	return r.fail[line]
}

// system module, one config root, and one deploy scaffold — the minimum `ggg
// new` accepts, since a project must name exactly one deployment and the
// bootstrap emitter requires the config capability.
func genesisTree(t *testing.T) string {
	t.Helper()
	root := selfHostTree(t)
	configBody := "package config\n\ntype Config struct{ Env string }\n"
	writeTestFile(t, root, "internal/config/config.go", []byte(configBody))
	writeTestFile(t, root, "registry/modules/system/config/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/config","kind":"system","name":"config","revision":1,"contract":1,
		"title":"Config","description":"The fixture config root.","requires":[],
		"files":[{"source":"internal/config/config.go","target":"internal/config/config.go",
		          "class":"go","sha256":"`+sha256Hex([]byte(configBody))+`","rewrite_module":true,"contract":true}],
		"claims":{"packages":["internal/config"]},
		"runtime":{"system":{"package":"internal/config","constructor":"NewModule","needs":[],
		           "provides":[{"field":"Config","capability":"config","type":"*config.Config"}],
		           "start":false,"stop":false}},
		"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	deployBody := "package fixture\n\nconst Version = 1\n"
	writeTestFile(t, root, "internal/deploy/fixture/fixture.go", []byte(deployBody))
	writeTestFile(t, root, "registry/modules/system/deploy-fixture/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/deploy-fixture","kind":"system","name":"deploy-fixture","revision":1,"contract":1,
		"title":"Deploy fixture","description":"A fixture deploy target.","requires":[],
		"files":[{"source":"internal/deploy/fixture/fixture.go","target":"internal/deploy/fixture/fixture.go",
		          "class":"go","sha256":"`+sha256Hex([]byte(deployBody))+`","rewrite_module":true,"contract":true}],
		"claims":{"packages":["internal/deploy/fixture"],"deploy":["fixture"]},
		"runtime":{"system":{"package":"internal/deploy/fixture","constructor":"NewModule","needs":[],"provides":[],"start":false,"stop":false},
		           "deploy":[{"id":"fixture","package":"internal/deploy/fixture","constructor":"NewDeployTarget"}]},
		"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	// Three declarations that exercise the posture rule: an example that
	// differs from its default is written, a secret is never written whatever
	// it declares, and an example equal to the default adds nothing.
	widgetBody := "package widget\n\nconst Version = 1\n"
	writeTestFile(t, root, "registry/modules/system/widget/module.json", []byte(`{"schema":2,"module":{
		"id":"ggg/system/widget","kind":"system","name":"widget","revision":1,"contract":1,
		"title":"Widget","description":"A widget system.","requires":[],
		"files":[{"source":"internal/widget/widget.go","target":"internal/widget/widget.go",
		          "class":"go","sha256":"`+sha256Hex([]byte(widgetBody))+`","rewrite_module":true,"contract":true}],
		"claims":{"packages":["internal/widget"],"environment":["FIXTURE_BYPASS","FIXTURE_LEVEL","FIXTURE_TOKEN"]},
		"runtime":{},"migrations":[],
		"environment":[
		  {"key":"FIXTURE_BYPASS","field":"FixtureBypass","type":"bool","description":"Local posture.","default":"false","example":"true"},
		  {"key":"FIXTURE_LEVEL","field":"FixtureLevel","type":"string","description":"Already defaulted.","default":"info","example":"info"},
		  {"key":"FIXTURE_TOKEN","field":"FixtureToken","type":"string","description":"A credential.","default":"","example":"never-write-me","secret":true}
		],
		"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	writeTestFile(t, root, "registry/systems.json", []byte(`{"schema":2,"kind":"system","items":[`+
		`"registry/modules/system/config/module.json",`+
		`"registry/modules/system/deploy-fixture/module.json","registry/modules/system/widget/module.json"]}`))
	writeTestFile(t, root, "registry/profiles.json", []byte(`{"schema":2,"kind":"profile","items":["registry/profiles/full.json"]}`))
	writeTestFile(t, root, "registry/profiles/full.json", []byte(`{"schema":2,"profile":{
		"id":"ggg/profile/full","kind":"profile","name":"full","revision":1,"contract":1,
		"title":"Full","description":"Every fixture module.",
		"members":["ggg/system/config","ggg/system/deploy-fixture","ggg/system/widget"],
		"required_provider_slots":[],"provider_defaults":{},"default_deployment":"ggg/system/deploy-fixture"}}`))
	return root
}

// runGenesis creates a project from the fixture registry through the one
// preview/apply boundary `ggg new` uses, with the task runner replaced so the
// ordered build-out is observable without driving a real toolchain.
func runGenesis(t *testing.T, runner TaskRunner) (string, error) {
	t.Helper()
	source := genesisTree(t)
	target := filepath.Join(t.TempDir(), "destination")
	controller := NewController(ControllerOptions{Root: source, Version: "v1.2.3", TaskRunner: runner})
	ctx := context.Background()
	plan, err := controller.Preview(ctx, NewMutation{
		Dir: target, Name: "genesis", ModulePath: "example.com/genesis",
		Profile: "ggg/profile/full", Registry: "directory:.",
	})
	if err != nil {
		return target, err
	}
	if _, err := controller.Apply(ctx, plan); err != nil {
		return target, err
	}
	return target, nil
}

// runGenesisInPlace initializes an existing, non-empty directory the way
// `ggg init` does, so the rollback branch that removes files from a directory
// the command did not create is actually exercised.
func runGenesisInPlace(t *testing.T, target string, runner TaskRunner) error {
	t.Helper()
	source := genesisTree(t)
	controller := NewController(ControllerOptions{Root: source, Version: "v1.2.3", TaskRunner: runner})
	ctx := context.Background()
	plan, err := controller.Preview(ctx, NewMutation{
		Dir: target, Name: "genesis", ModulePath: "example.com/genesis",
		Profile: "ggg/profile/full", Registry: "directory:.", InPlace: true,
	})
	if err != nil {
		return err
	}
	_, err = controller.Apply(ctx, plan)
	return err
}

// The in-place rollback is the only branch that deletes from a directory the
// command did not create, so it is the one that has to be shown deleting
// exactly its own writes. An operator's pre-existing file is not ours and
// must come back byte-identical.
func TestInPlaceGenesisRollbackRemovesOnlyWhatItWrote(t *testing.T) {
	target := t.TempDir()
	operator := []byte("notes the installer has no claim on\n")
	writeTestFile(t, target, "NOTES.md", operator)
	runner := &recordingRunner{fail: map[string]error{"go build ./...": os.ErrInvalid}}

	err := runGenesisInPlace(t, target, runner)
	if err == nil {
		t.Fatal("in-place genesis reported success over a tree that does not compile")
	}
	if exitOf(t, err) != exitRollback {
		t.Fatalf("exit = %d, want %d (rolled back)", exitOf(t, err), exitRollback)
	}
	if !strings.Contains(err.Error(), "go.sum") {
		t.Fatalf("the in-place remedy must name go.sum, which the rollback cannot remove: %v", err)
	}
	// The directory itself survives — it was not ours to delete.
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("in-place rollback removed a directory it did not create: %v", statErr)
	}
	for _, name := range []string{
		"go.mod", modkit.ProjectFileName, modkit.LockFileName,
		filepath.Join("internal", "widget", "widget.go"),
		filepath.Join("internal", "config", "config.go"),
		filepath.Join("internal", "modules", "bootstrap_registry_gen.go"),
	} {
		if _, statErr := os.Stat(filepath.Join(target, filepath.FromSlash(name))); !os.IsNotExist(statErr) {
			t.Fatalf("in-place rollback kept %s (%v)", name, statErr)
		}
	}
	kept, readErr := os.ReadFile(filepath.Join(target, "NOTES.md"))
	if readErr != nil {
		t.Fatalf("in-place rollback removed the operator's own file: %v", readErr)
	}
	if string(kept) != string(operator) {
		t.Fatalf("operator file = %q, want %q", kept, operator)
	}
}

// The build-out is what makes the created tree compile, and the compile check
// is what makes the success envelope mean something. Both run in the created
// project, never in the directory `ggg new` was invoked from.
func TestNewGeneratesToolOutputsAndVerifiesTheTreeCompiles(t *testing.T) {
	runner := &recordingRunner{}
	target, err := runGenesis(t, runner)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	// The fixture declares no Go tools and no tool artifacts, so its
	// generator set is empty; the require-graph completion and the compile
	// check are unconditional.
	want := []string{"go mod tidy -e", "go mod tidy", "go build ./..."}
	if len(runner.calls) != len(want) {
		t.Fatalf("genesis ran %v, want %v", runner.calls, want)
	}
	for i, argv := range want {
		if runner.calls[i] != argv {
			t.Fatalf("genesis step %d = %q, want %q (all: %v)", i, runner.calls[i], argv, runner.calls)
		}
		if runner.roots[i] != target {
			t.Fatalf("genesis step %q ran in %q, want the created project %q", argv, runner.roots[i], target)
		}
	}
	if _, statErr := os.Stat(filepath.Join(target, modkit.LockFileName)); statErr != nil {
		t.Fatalf("genesis kept no lock: %v", statErr)
	}
}

// A created project must boot in the posture its own documentation and the
// clean-project gate assume: /dev/login reachable with zero SaaS accounts.
// The env files were created empty, so DEV_AUTH_BYPASS was unset, /login
// self-redirected, and `ggg test smoke` failed on a project that had done
// nothing wrong. .env.example carries the posture but nothing reads it —
// .ggg/env/<environment>.env is what compose and the CLI actually load.
func TestNewSeedsTheDeclaredDevelopmentPosture(t *testing.T) {
	target, err := runGenesis(t, &recordingRunner{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	for _, environment := range []string{"development", "test"} {
		path := filepath.Join(target, ".ggg", "env", environment+".env")
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("genesis left no %s env file: %v", environment, statErr)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("%s env mode = %04o, want 0600", environment, got)
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(body) != "FIXTURE_BYPASS=true\n" {
			t.Fatalf("%s env = %q, want only the declared non-secret posture", environment, body)
		}
		// A secret is never written, whatever its declaration states, and a
		// key already at its default adds nothing.
		if strings.Contains(string(body), "FIXTURE_TOKEN") || strings.Contains(string(body), "never-write-me") {
			t.Fatalf("%s env leaked a declared secret: %q", environment, body)
		}
		if strings.Contains(string(body), "FIXTURE_LEVEL") {
			t.Fatalf("%s env restated a compiled default: %q", environment, body)
		}
	}
	// Production configuration is never written to disk.
	if _, statErr := os.Stat(filepath.Join(target, ".ggg", "env", "production.env")); !os.IsNotExist(statErr) {
		t.Fatalf("genesis wrote a production env file (%v)", statErr)
	}
}

// `ggg provider configure` owns the file after creation, so a value an
// operator put there must survive every later run that ensures the file
// exists.
func TestEnvironmentFileSeedNeverClobbersOperatorValues(t *testing.T) {
	target, err := runGenesis(t, &recordingRunner{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path := filepath.Join(target, ".ggg", "env", "development.env")
	operator := []byte("FIXTURE_BYPASS=false\nOPERATOR_ONLY=kept\n")
	if err := os.WriteFile(path, operator, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureEnvironmentFile(target, "development"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(operator) {
		t.Fatalf("env file = %q, want the operator's bytes %q", body, operator)
	}
}

// A success envelope over a tree that does not compile is the defect this
// guards: `ggg setup` there cannot build bin/ggg, so nothing is left to run.
// The genesis rolls back instead, and the diagnostic names the check.
func TestNewRollsBackWhenTheCreatedTreeDoesNotCompile(t *testing.T) {
	runner := &recordingRunner{fail: map[string]error{"go build ./...": os.ErrInvalid}}
	target, err := runGenesis(t, runner)
	if err == nil {
		t.Fatal("new reported success over a tree that does not compile")
	}
	if exitOf(t, err) != exitRollback {
		t.Fatalf("new exit = %d, want %d (rolled back)", exitOf(t, err), exitRollback)
	}
	if !strings.Contains(err.Error(), "go build ./...") || !strings.Contains(err.Error(), "re-run `ggg new`") {
		t.Fatalf("new error %q names neither the failed check nor the remedy", err)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("rolled-back genesis left %s behind (%v)", target, statErr)
	}
}

// The generators are gated on the declaration that produces each one, so both
// `ggg generate` and genesis run the installed project's pipeline rather than
// a constant: a project that installs neither templ nor sqlc runs neither.
func TestGenerationStepsFollowTheInstalledDeclarations(t *testing.T) {
	if steps := generationSteps(modkit.Lock{Schema: 2}); len(steps) != 0 {
		t.Fatalf("a lock that declares no generators produced %v", steps)
	}
	lock := modkit.Lock{
		Schema:  2,
		GoTools: []string{sqlcGoTool, templGoTool},
		Modules: []modkit.LockedModule{{Manifest: modkit.Manifest{
			ID: "ggg/system/dev-tools",
			Dependencies: modkit.Dependencies{Tools: []modkit.ToolArtifact{
				{InstallPath: tailwindInstallPath},
			}},
		}}},
	}
	got := make([]string, 0, 3)
	for _, argv := range generationSteps(lock) {
		got = append(got, strings.Join(argv, " "))
	}
	want := []string{
		"go tool templ generate", "go tool sqlc generate",
		filepath.FromSlash(tailwindInstallPath) + " -i input.css -o static/app.css --minify",
	}
	if len(got) != len(want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("step %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// failingWriter refuses every write the way a closed pipe does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

// A command whose output never reached the caller must not report success.
// The help and --help paths discarded the write error and returned nil, so
// `ggg --help` into a closed pipe printed nothing and exited 0 — the same
// status-versus-reality disagreement the trusted-task envelope had.
func TestHelpDoesNotReportSuccessWhenItsOutputCannotBeWritten(t *testing.T) {
	for _, argv := range [][]string{{"--help"}, {"-h"}, {"sync", "--help"}} {
		var errOut bytesBuffer
		app := App{Out: failingWriter{}, Err: &errOut, Root: t.TempDir(), Version: "v1.2.3"}
		err := app.Run(context.Background(), argv)
		if err == nil {
			t.Fatalf("%v reported success over output that was never written", argv)
		}
		// ExitCode is what cmd/ggg exits with; an uncoded write failure is a
		// runtime failure, never success.
		if got := ExitCode(err); got != exitRuntime {
			t.Fatalf("%v exit = %d, want %d", argv, got, exitRuntime)
		}
	}
}

// A created project must be idempotent-clean: `ggg check` is what the
// generated README, Makefile and docs all point a new operator at, and it
// begins with `sync --check --offline`.
//
// It was not. `ggg new` planned against the operator's core registry alone and
// then wrote gogogadget.json with the core AND the project-local registry, so
// the lock described a registry set the project did not have. The registry
// commit is a digest over the resolved sources, and every generated header
// carries it, so an untouched fresh destination reported a pending lock change
// and a generated drift for every emitted file.
func TestFreshGenesisIsIdempotentClean(t *testing.T) {
	target, err := runGenesis(t, &recordingRunner{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// The project-local registry is in the intent file and in the lock, and
	// the lock resolved it rather than merely listing it.
	project, err := modkit.ParseProject(readFileForTest(t, filepath.Join(target, modkit.ProjectFileName)))
	if err != nil {
		t.Fatal(err)
	}
	lock, err := modkit.ParseLock(readFileForTest(t, filepath.Join(target, modkit.LockFileName)))
	if err != nil {
		t.Fatal(err)
	}
	local := ""
	for _, registry := range project.Registries {
		if registry.Path == "registry" {
			local = registry.Namespace
		}
	}
	if local == "" {
		t.Fatalf("project declares no project-local registry: %+v", project.Registries)
	}
	if len(lock.Registries) != len(project.Registries) {
		t.Fatalf("lock records %d registries, project declares %d", len(lock.Registries), len(project.Registries))
	}
	found := false
	for _, recorded := range lock.Registries {
		if recorded.Namespace == local {
			found = true
		}
	}
	if !found {
		t.Fatalf("lock does not record the project-local registry %q: %+v", local, lock.Registries)
	}
	if _, ok := lockedSnapshotNamespaces(lock)[local]; !ok {
		t.Fatalf("lock has no snapshot for the project-local registry %q", local)
	}

	// The destination's own check: no pending changes, no generated drift.
	assertNoPendingChanges(t, target)
}

// The project-local registry must be a working source, not a declaration. An
// operator's next step is `ggg create`, which authors into it, so a module
// written there has to resolve and install.
func TestGenesisProjectLocalRegistryResolvesAnAuthoredModule(t *testing.T) {
	target, err := runGenesis(t, &recordingRunner{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	project, err := modkit.ParseProject(readFileForTest(t, filepath.Join(target, modkit.ProjectFileName)))
	if err != nil {
		t.Fatal(err)
	}
	namespace := ""
	for _, registry := range project.Registries {
		if registry.Path == "registry" {
			namespace = registry.Namespace
		}
	}
	if namespace == "" {
		t.Fatal("project declares no project-local registry")
	}

	body := "package authored\n\nconst Version = 1\n"
	writeTestFile(t, target, "registry/registry/modules/system/authored/authored.go", []byte(body))
	writeTestFile(t, target, "registry/registry/modules/system/authored/module.json", []byte(`{"schema":2,"module":{
		"id":"`+namespace+`/system/authored","kind":"system","name":"authored","revision":1,"contract":1,
		"title":"Authored","description":"A module the operator authored.","requires":[],
		"files":[{"source":"registry/modules/system/authored/authored.go",
		          "target":"internal/authored/authored.go","class":"go",
		          "sha256":"`+sha256Hex([]byte(body))+`","rewrite_module":true,"contract":false}],
		"claims":{"packages":["internal/authored"]},
		"runtime":{},"migrations":[],"environment":[],"docs":[],"tests":{},
		"data":[],"dependencies":{"go":[],"tools":[],"containers":[]},"removal_policy":"free"}}`))
	if _, _, err := modkit.BuildRegistryIndexes(filepath.Join(target, "registry")); err != nil {
		t.Fatalf("build local registry indexes: %v", err)
	}

	controller := NewController(ControllerOptions{Root: target, Version: "v1.2.3", TaskRunner: &recordingRunner{}})
	plan, err := controller.Preview(context.Background(), GraphMutation{Kind: modkit.OpAdd, Modules: []string{namespace + "/system/authored"}})
	if err != nil {
		t.Fatalf("add the authored module: %v", err)
	}
	if _, err := controller.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply the authored module: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(target, "internal", "authored", "authored.go")); statErr != nil {
		t.Fatalf("the authored module's payload was not installed: %v", statErr)
	}
	assertNoPendingChanges(t, target)
}

// assertNoPendingChanges runs the destination's own `sync --check --offline`,
// which is the first step of `ggg check`.
func assertNoPendingChanges(t *testing.T, target string) {
	t.Helper()
	controller := NewController(ControllerOptions{Root: target, Version: "v1.2.3", TaskRunner: &recordingRunner{}})
	plan, previewErr := controller.Preview(context.Background(), SyncMutation{Check: true, Offline: true})
	if previewErr != nil {
		t.Fatalf("sync --check --offline on an untouched destination: %v", previewErr)
	}
	pending := 0
	for _, change := range plan.Local.Changes {
		if change.Kind != modkit.ChangeUnchanged {
			pending++
			t.Errorf("pending %s %s %s", change.Kind, change.Class, change.Path)
		}
	}
	for _, diagnostic := range plan.Diagnostics {
		t.Errorf("diagnostic %s %s", diagnostic.Code, diagnostic.Message)
	}
	if pending != 0 {
		t.Fatalf("a fresh destination reported %d pending change(s); `ggg check` exits 4 there", pending)
	}
}

func lockedSnapshotNamespaces(lock modkit.Lock) map[string]struct{} {
	out := make(map[string]struct{}, len(lock.Snapshots))
	for _, snapshot := range lock.Snapshots {
		out[snapshot.Namespace] = struct{}{}
	}
	return out
}

func readFileForTest(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
