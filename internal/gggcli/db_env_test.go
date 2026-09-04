package gggcli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogogadget/gogogadget/internal/modkit"
)

// `ggg db` handed goose `os.Getenv("DATABASE_URL")` straight in its argv, so a
// project whose value lives only where the CLI writes it —
// .ggg/env/<environment>.env — passed an EMPTY DSN. That is not an error to
// libpq: it falls back to its own defaults and connects to a local socket, so
// a generated project's documented first migration could aim at a server the
// project has nothing to do with. These tests pin the precedence, the refusal,
// and that no tool is ever invoked with an empty connection string.

const (
	fileDSN = "postgres://postgres:postgres@localhost:55432/gogogadget?sslmode=disable"
	envDSN  = "postgres://postgres:postgres@localhost:6000/from-process-env?sslmode=disable"
	dotDSN  = "postgres://postgres:postgres@localhost:6001/from-legacy-dotenv?sslmode=disable"
)

// dbProject writes a project root carrying the CLI-managed env files named by
// contents, keyed by environment.
func dbProject(t *testing.T, contents map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for environment, body := range contents {
		writeTestFile(t, root, filepath.Join(".ggg", "env", environment+".env"), []byte(body))
	}
	return root
}

// driveDBTask drives one `ggg db ACTION` through the controller's own
// preview/apply boundary and returns the recorded argv and injected env.
func driveDBTask(t *testing.T, root, action, environment string) (*recordingRunner, error) {
	t.Helper()
	runner := &recordingRunner{}
	controller := NewController(ControllerOptions{Root: root, Version: "v1.2.3", TaskRunner: runner})
	plan, err := controller.Preview(context.Background(),
		TaskMutation{Task: "db", Action: action, Environment: environment})
	if err != nil {
		return runner, err
	}
	_, err = controller.Apply(context.Background(), plan)
	return runner, err
}

// injectedFor returns the environment injected with the argv containing want.
func injectedFor(t *testing.T, runner *recordingRunner, want string) map[string]string {
	t.Helper()
	for i, call := range runner.calls {
		if strings.Contains(call, want) {
			return runner.envs[i]
		}
	}
	t.Fatalf("no task ran %q; ran %v", want, runner.calls)
	return nil
}

// The failure that shipped: the value is only in the CLI-managed file.
func TestDBTasksResolveTheDSNFromTheCLIManagedFile(t *testing.T) {
	root := dbProject(t, map[string]string{"development": "DATABASE_URL=" + fileDSN + "\n"})
	for _, testCase := range []struct {
		action string
		argv   string
		key    string
	}{
		{"migrate", "goose", "GOOSE_DBSTRING"},
		{"status", "goose", "GOOSE_DBSTRING"},
		{"seed", "cmd/seed", "DATABASE_URL"},
	} {
		t.Run(testCase.action, func(t *testing.T) {
			runner, err := driveDBTask(t, root, testCase.action, "")
			if err != nil {
				t.Fatalf("db %s: %v", testCase.action, err)
			}
			injected := injectedFor(t, runner, testCase.argv)
			if injected[testCase.key] != fileDSN {
				t.Fatalf("injected %s = %q, want the file's value %q", testCase.key, injected[testCase.key], fileDSN)
			}
			// The DSN carries a password; the process list is public. It must
			// never reach argv, and no argument may be empty.
			for _, call := range runner.calls {
				if strings.Contains(call, fileDSN) {
					t.Fatalf("the connection string reached argv: %q", call)
				}
			}
			for i, call := range runner.calls {
				for _, arg := range strings.Split(call, " ") {
					if arg == "" {
						t.Fatalf("task %d was invoked with an empty argument: %q", i, call)
					}
				}
			}
		})
	}
}

// Process environment wins over the file, and the file wins over legacy .env.
func TestDBTaskDSNPrecedence(t *testing.T) {
	root := dbProject(t, map[string]string{"development": "DATABASE_URL=" + fileDSN + "\n"})
	writeTestFile(t, root, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))

	t.Setenv("DATABASE_URL", envDSN)
	runner, err := driveDBTask(t, root, "migrate", "")
	if err != nil {
		t.Fatalf("db migrate: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != envDSN {
		t.Fatalf("injected %q, want the process environment to win with %q", got, envDSN)
	}

	// With the process value absent, the CLI-managed file outranks .env.
	t.Setenv("DATABASE_URL", "")
	runner, err = driveDBTask(t, root, "migrate", "")
	if err != nil {
		t.Fatalf("db migrate without a process value: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != fileDSN {
		t.Fatalf("injected %q, want the CLI-managed file's %q to outrank .env", got, fileDSN)
	}
}

// `--environment test` reads the test file, and test never reads .env: a
// legacy dotenv is a development convenience and pointing the test database at
// it would be the same wrong-server hazard.
func TestDBTaskEnvironmentSelectsTheFileAndTestIgnoresDotEnv(t *testing.T) {
	root := dbProject(t, map[string]string{
		"development": "DATABASE_URL=" + fileDSN + "\n",
		"test":        "DATABASE_URL=" + envDSN + "\n",
	})
	writeTestFile(t, root, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))
	t.Setenv("DATABASE_URL", "")

	runner, err := driveDBTask(t, root, "status", "test")
	if err != nil {
		t.Fatalf("db status --environment test: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != envDSN {
		t.Fatalf("injected %q, want the test file's %q", got, envDSN)
	}

	// A test environment with no test file must refuse rather than fall
	// through to the development file or to .env.
	bare := t.TempDir()
	writeTestFile(t, bare, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))
	writeTestFile(t, bare, filepath.Join(".ggg", "env", "development.env"), []byte("DATABASE_URL="+fileDSN+"\n"))
	if _, err := driveDBTask(t, bare, "status", "test"); err == nil {
		t.Fatal("db status --environment test resolved a value from a development-only source")
	}
}

// The dangerous case: nothing supplies the value. It must refuse, and no tool
// may be invoked at all — passing empty is how a command reaches the wrong
// server.
func TestDBTasksRefuseAnUnresolvedDSNWithoutInvokingATool(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	for _, action := range []string{"migrate", "status", "seed"} {
		t.Run(action, func(t *testing.T) {
			runner, err := driveDBTask(t, dbProject(t, nil), action, "")
			if err == nil {
				t.Fatalf("db %s succeeded with no connection string anywhere", action)
			}
			if exitOf(t, err) != exitRefusal {
				t.Fatalf("db %s exit = %d, want %d (refusal)", action, exitOf(t, err), exitRefusal)
			}
			for _, want := range []string{"DATABASE_URL", "development", filepath.ToSlash(filepath.Join(".ggg", "env", "development.env"))} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not name %q", err, want)
				}
			}
			if len(runner.calls) != 0 {
				t.Fatalf("a tool ran despite the unresolved value: %v", runner.calls)
			}
		})
	}
}

// Production configuration comes from the deployment environment. A file
// someone left at .ggg/env/production.env must not be read, so the absence of
// the file is not what the rule rests on.
func TestProductionReadsNoEnvironmentFile(t *testing.T) {
	root := dbProject(t, map[string]string{"production": "DATABASE_URL=" + fileDSN + "\n"})
	t.Setenv("DATABASE_URL", "")
	controller := NewController(ControllerOptions{Root: root, Version: "v1.2.3", TaskRunner: &recordingRunner{}})
	if _, _, err := controller.taskEnvValue("production", "DATABASE_URL"); err == nil {
		t.Fatal("production resolved a value from .ggg/env/production.env")
	}
	if _, err := os.Stat(filepath.Join(root, ".ggg", "env", "production.env")); err != nil {
		t.Fatalf("the fixture must actually have the file, or this proves nothing: %v", err)
	}
}

// declaredDSN is DATABASE_URL's declared default: a live address on any
// machine that has ever run Postgres locally. The default is deliberate — it
// matches the documented development posture and the zero-account path
// depends on it — but it is a documented guess, not something an operator or
// the project said.
const declaredDSN = "postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable"

// dbFixtureManifest is one lock-embedded manifest, spelled out because the
// codec validates the shape.
func dbFixtureManifest(id, name string, envs []modkit.EnvironmentVariable, runtime modkit.RuntimeContributions) modkit.Manifest {
	return modkit.Manifest{
		ID: id, Kind: modkit.ModuleSystem, Name: name,
		Revision: 1, Contract: 1, Title: name, Description: "Fixture module.",
		Requires: []modkit.Requirement{}, Files: []modkit.ManifestFile{}, Claims: modkit.NamespaceClaims{},
		Runtime: runtime, Migrations: []modkit.ManifestMigration{},
		Docs: []modkit.DocumentationRef{}, Data: []modkit.DataDeclaration{},
		Dependencies:  modkit.Dependencies{Go: []modkit.GoDependency{}, Tools: []modkit.ToolArtifact{}, Containers: []modkit.ContainerDependency{}},
		RemovalPolicy: "free",
		Environment:   envs,
	}
}

// writeDBLock installs a lock declaring DATABASE_URL's default and, when
// providers is non-empty, a selected database adapter whose local service is
// what the derivation reads.
func writeDBLock(t *testing.T, root string, providers map[string]modkit.ProviderSelections, manifests ...modkit.Manifest) {
	t.Helper()
	order := make([]string, 0, len(manifests))
	modules := make([]modkit.LockedModule, 0, len(manifests))
	for _, manifest := range manifests {
		order = append(order, manifest.ID)
		modules = append(modules, modkit.LockedModule{
			ID: manifest.ID, Revision: 1, Contract: 1,
			RegistryNamespace: "ggg", SourceCommit: strings.Repeat("b", 40),
			SnapshotSHA256: strings.Repeat("c", 64), Reason: "explicit", RequiredBy: []string{},
			Files: []modkit.LockedFile{}, Migrations: []modkit.LockedMigration{},
			Manifest: manifest,
		})
	}
	data, err := modkit.MarshalLock(modkit.Lock{
		Schema: 2, RegistryCommit: strings.Repeat("a", 64),
		Registries: []modkit.LockedRegistry{}, Snapshots: []modkit.LockedSnapshot{},
		Order: order, Dependencies: []modkit.LockedDependency{},
		Providers: providers, Modules: modules,
	})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, modkit.LockFileName, data)
}

// databaseSeamManifest declares the key and its default; the adapter declares
// the local service. That is the real split: the seam owns configuration, the
// adapter owns the service.
func databaseSeamManifest() modkit.Manifest {
	return dbFixtureManifest("ggg/system/database", "database", []modkit.EnvironmentVariable{{
		Key: "DATABASE_URL", Field: "DatabaseURL", Type: modkit.EnvString,
		Description: "Postgres connection string.", Default: declaredDSN,
	}}, modkit.RuntimeContributions{})
}

func databaseAdapterManifest() modkit.Manifest {
	return dbFixtureManifest("ggg/system/database-postgres", "database-postgres",
		[]modkit.EnvironmentVariable{},
		modkit.RuntimeContributions{System: &modkit.SystemContribution{
			Package:     "internal/db/postgres",
			Constructor: "NewModule",
			Needs:       []modkit.RuntimeNeed{},
			Provides:    []modkit.RuntimeProvide{{Field: "Pool", Capability: "database.pool", Type: "*pgxpool.Pool"}},
			Adapter: &modkit.AdapterContribution{Slot: "ggg/database", Targets: []modkit.ServiceTarget{{
				ID: "docker-postgres", Title: "Docker Postgres", Mode: "development", Automation: "manual",
				DocsURL: "https://example.test/docs", Environments: []string{"development", "test"},
				Inputs: []modkit.TargetInput{},
				LocalService: &modkit.LocalService{
					Container:   "postgres@sha256:" + strings.Repeat("d", 64),
					Ports:       []modkit.LocalServicePort{{Name: "postgres", Container: 5432, DefaultHost: 5432}},
					Environment: []modkit.LocalServiceEnv{}, Volumes: []modkit.LocalServiceVolume{},
					Health: modkit.LocalServiceHealth{Kind: "tcp", Port: 5432},
				},
			}}},
		}})
}

func dockerPostgresSelections() map[string]modkit.ProviderSelections {
	choice := modkit.ProviderSelection{Adapter: "ggg/system/database-postgres", Target: "docker-postgres"}
	return map[string]modkit.ProviderSelections{"ggg/database": {Development: choice, Test: choice}}
}

// The default is a documented guess, so a command that MUTATES must refuse it
// while a command that reads may use it.
//
// Mutation: accept envDeclaredDefault for migrate and seed, and a fresh
// project's first `ggg db migrate` migrates whatever answers on
// localhost:5432 — which on a machine running its own Postgres is not this
// project's database.
func TestMutatingDBFormsRefuseADefaultSourcedDSN(t *testing.T) {
	root := t.TempDir()
	// No providers: nothing to derive, so the declared default is the only
	// value available. That is the state this refusal exists for.
	writeDBLock(t, root, nil, databaseSeamManifest())
	t.Setenv("DATABASE_URL", "")

	// Reading is allowed, and it uses the declared default.
	runner, err := driveDBTask(t, root, "status", "")
	if err != nil {
		t.Fatalf("db status on the declared default: %v", err)
	}
	if got := injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]; got != declaredDSN {
		t.Fatalf("db status injected %q, want the declared default %q", got, declaredDSN)
	}

	// Mutating is refused, and no tool runs.
	for _, action := range []string{"migrate", "seed"} {
		t.Run(action, func(t *testing.T) {
			runner, err := driveDBTask(t, root, action, "")
			if err == nil {
				t.Fatalf("db %s mutated a database supplied only by the declared default", action)
			}
			if exitOf(t, err) != exitRefusal {
				t.Fatalf("db %s exit = %d, want %d", action, exitOf(t, err), exitRefusal)
			}
			for _, want := range []string{"declared default", declaredDSN, "provider configure", "db status"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("refusal %q does not mention %q", err, want)
				}
			}
			if len(runner.calls) != 0 {
				t.Fatalf("a tool ran despite the refusal: %v", runner.calls)
			}
		})
	}

	// A configured value is trusted again, even when it equals nothing special.
	t.Setenv("DATABASE_URL", fileDSN)
	if _, err := driveDBTask(t, root, "migrate", ""); err != nil {
		t.Fatalf("db migrate with a configured value: %v", err)
	}
}

// `ggg db migrate --environment test` used to refuse until an operator
// hand-wrote a DSN, even though the project knows its test stack publishes
// 15432. The refusal was right and the reason was wrong: there was no derived
// value, not no value. A derived one reflects this project's selected adapter
// and this environment's published port, so it is trustworthy enough to
// mutate with.
//
// Mutation: treat a derived value as envDeclaredDefault, and both mutating
// forms refuse in a project that has said exactly which database it means.
func TestDBTasksMutateThroughTheDerivedDSN(t *testing.T) {
	root := t.TempDir()
	writeDBLock(t, root, dockerPostgresSelections(), databaseSeamManifest(), databaseAdapterManifest())
	t.Setenv("DATABASE_URL", "")

	for environment, want := range map[string]string{
		"development": "postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable",
		"test":        "postgres://postgres:postgres@localhost:15432/gogogadget?sslmode=disable",
	} {
		t.Run(environment, func(t *testing.T) {
			for _, testCase := range []struct{ action, argv, key string }{
				{"migrate", "goose", "GOOSE_DBSTRING"},
				{"seed", "cmd/seed", "DATABASE_URL"},
			} {
				runner, err := driveDBTask(t, root, testCase.action, environment)
				if err != nil {
					t.Fatalf("db %s --environment %s: %v", testCase.action, environment, err)
				}
				if got := injectedFor(t, runner, testCase.argv)[testCase.key]; got != want {
					t.Fatalf("db %s --environment %s injected %q, want the derived %q",
						testCase.action, environment, got, want)
				}
			}
		})
	}
}

// The full precedence, one step per assertion: process environment, then the
// CLI-managed file, then the legacy .env in development only, then derived,
// then the declared default.
//
// Mutation: move the derived layer above remote.LookupEnv, and an operator's
// own exported DSN — or the one `ggg provider configure` wrote — is silently
// overruled by the project's default stack address.
func TestDBTaskDSNPrecedenceIncludesTheDerivedLayer(t *testing.T) {
	const derivedDSN = "postgres://postgres:postgres@localhost:5432/gogogadget?sslmode=disable"
	root := dbProject(t, map[string]string{"development": "DATABASE_URL=" + fileDSN + "\n"})
	writeDBLock(t, root, dockerPostgresSelections(), databaseSeamManifest(), databaseAdapterManifest())
	writeTestFile(t, root, ".env", []byte("DATABASE_URL="+dotDSN+"\n"))

	resolved := func(t *testing.T) string {
		t.Helper()
		runner, err := driveDBTask(t, root, "migrate", "")
		if err != nil {
			t.Fatalf("db migrate: %v", err)
		}
		return injectedFor(t, runner, "goose")["GOOSE_DBSTRING"]
	}

	t.Setenv("DATABASE_URL", envDSN)
	if got := resolved(t); got != envDSN {
		t.Fatalf("injected %q, want the process environment to win with %q", got, envDSN)
	}

	t.Setenv("DATABASE_URL", "")
	if got := resolved(t); got != fileDSN {
		t.Fatalf("injected %q, want the CLI-managed file's %q to outrank .env", got, fileDSN)
	}

	if err := os.Remove(filepath.Join(root, ".ggg", "env", "development.env")); err != nil {
		t.Fatal(err)
	}
	if got := resolved(t); got != dotDSN {
		t.Fatalf("injected %q, want the legacy .env's %q to outrank the derived value", got, dotDSN)
	}

	if err := os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if got := resolved(t); got != derivedDSN {
		t.Fatalf("injected %q, want the derived %q", got, derivedDSN)
	}

	// With the selection gone there is nothing to derive, so the declared
	// default is all that is left — and it is refused for a mutation.
	writeDBLock(t, root, nil, databaseSeamManifest())
	if _, err := driveDBTask(t, root, "migrate", ""); err == nil || exitOf(t, err) != exitRefusal {
		t.Fatalf("db migrate with only the declared default = %v, want a refusal", err)
	}
}

// Production derives nothing: it has no generated Compose file and no local
// service, so there is no host address to name. Combined with reading no file
// there, a production `ggg db` form has exactly one legitimate source — the
// deployment environment.
//
// Mutation: let DerivedEnvironmentValues plan production like any other
// environment, and a production command resolves a localhost DSN.
func TestProductionDerivesNoHostDSN(t *testing.T) {
	root := t.TempDir()
	writeDBLock(t, root, dockerPostgresSelections(), databaseSeamManifest(), databaseAdapterManifest())
	t.Setenv("DATABASE_URL", "")
	controller := NewController(ControllerOptions{Root: root, Version: "v1.2.3", TaskRunner: &recordingRunner{}})
	value, provenance, err := controller.taskEnvValue("production", "DATABASE_URL")
	if err != nil {
		t.Fatalf("production resolution: %v", err)
	}
	if provenance != envDeclaredDefault || value != declaredDSN {
		t.Fatalf("production resolved %q from provenance %d, want the declared default", value, provenance)
	}
}

// N2. `c.runner()` handed the default task runner os.Stdout, one function
// above the genesisRunner that was deliberately given os.Stderr for exactly
// this reason. Every trusted task shells out — setup, generate, check, test,
// db, services, build — so tool output landed on the same stream as the
// envelope and `--json` stopped being parseable.
//
// This drives the real thing: no injected runner, the process's own stdout and
// stderr swapped for files, and tasks that really invoke tools. `test unit`
// runs `go test ./...` in a throwaway module whose one package PASSES, so the
// child writes "ok <package>" to ITS stdout — which is exactly the byte
// stream that used to prefix the envelope. `db status` covers the other shape,
// a child that fails.
//
// Mutation: point osTaskRunner's out back at os.Stdout, and `go test`'s ok
// line lands ahead of the envelope; json.Decode then fails outright.
func TestJSONTrustedTaskEmitsExactlyOneDocumentOnStdout(t *testing.T) {
	root := t.TempDir()
	writeDBLock(t, root, dockerPostgresSelections(), databaseSeamManifest(), databaseAdapterManifest())
	// A configured value, so `db status` reaches its tool rather than
	// refusing: this test is about streams, not resolution.
	t.Setenv("DATABASE_URL", fileDSN)
	writeTestFile(t, root, "go.mod", []byte("module example.test/streams\n\ngo 1.21\n"))
	writeTestFile(t, root, filepath.Join("pkg", "pkg.go"), []byte("package pkg\n"))
	writeTestFile(t, root, filepath.Join("pkg", "pkg_test.go"),
		[]byte("package pkg\n\nimport \"testing\"\n\nfunc TestPasses(t *testing.T) {}\n"))

	for _, task := range [][]string{{"test", "unit"}, {"db", "status"}} {
		t.Run(strings.Join(task, " "), func(t *testing.T) {
			stdout, stderr := captureProcessStreams(t, func() {
				app := App{Out: os.Stdout, Err: os.Stderr, Root: root, Version: "v1.2.3"}
				_ = app.Run(context.Background(), append(task, "--json"))
			})
			decoder := json.NewDecoder(strings.NewReader(stdout))
			var envelope map[string]any
			if err := decoder.Decode(&envelope); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\nstdout: %q\nstderr: %q", err, stdout, stderr)
			}
			if _, err := decoder.Token(); err != io.EOF {
				t.Fatalf("stdout carries more than the envelope (%v)\nstdout: %q", err, stdout)
			}
			if envelope["command"] == nil {
				t.Fatalf("stdout is not the envelope: %q", stdout)
			}
			// The tool's own output has to go somewhere, or this would pass by
			// running nothing at all.
			if strings.TrimSpace(stderr) == "" {
				t.Fatalf("no subprocess output reached stderr; the task cannot have run")
			}
		})
	}
}

// Both task runners send subprocess stdout to stderr. genesisRunner has always
// done so; runner() is the one that did not, and the two must not diverge —
// `ggg new --json` and `ggg setup --json` make the same promise.
//
// Mutation: change either runner's out to os.Stdout and this fails without
// needing a subprocess.
func TestBothTaskRunnersSendSubprocessOutputToStderr(t *testing.T) {
	controller := NewController(ControllerOptions{Root: t.TempDir(), Version: "v1.2.3"})
	for name, runner := range map[string]TaskRunner{
		"runner":        controller.runner(),
		"genesisRunner": controller.genesisRunner(),
	} {
		backed, ok := runner.(osTaskRunner)
		if !ok {
			t.Fatalf("%s is not the os-backed runner: %T", name, runner)
		}
		if backed.out != os.Stderr || backed.err != os.Stderr {
			t.Fatalf("%s does not route both subprocess streams to stderr", name)
		}
	}
}

// captureProcessStreams runs fn with the process's own stdout and stderr
// replaced by files, and returns what each received. The files are the point:
// a subprocess inherits file descriptors, so an in-memory writer would prove
// nothing about where a child's output goes.
func captureProcessStreams(t *testing.T, fn func()) (string, string) {
	t.Helper()
	originalOut, originalErr := os.Stdout, os.Stderr
	defer func() { os.Stdout, os.Stderr = originalOut, originalErr }()
	outFile, errFile := newStreamFile(t, "stdout"), newStreamFile(t, "stderr")
	os.Stdout, os.Stderr = outFile, errFile
	fn()
	os.Stdout, os.Stderr = originalOut, originalErr
	return readStreamFile(t, outFile), readStreamFile(t, errFile)
}

func newStreamFile(t *testing.T, name string) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	return file
}

func readStreamFile(t *testing.T, file *os.File) string {
	t.Helper()
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
